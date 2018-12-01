package hostdb

import (
	"math"
	"math/big"

	"github.com/HyperspaceApp/Hyperspace/build"
	"github.com/HyperspaceApp/Hyperspace/modules"
	"github.com/HyperspaceApp/Hyperspace/modules/renter/hostdb/hosttree"
	"github.com/HyperspaceApp/Hyperspace/types"
)

var (
	// Because most weights would otherwise be fractional, we set the base
	// weight to be very large.
	baseWeight = types.NewCurrency(new(big.Int).Exp(big.NewInt(10), big.NewInt(80), nil))

	// collateralExponentiation is the power to which we raise the weight
	// during collateral adjustment when the collateral is large. This sublinear
	// number ensures that there is not an overpreference on collateral when
	// collateral is large relative to the size of the allowance.
	collateralExponentiationLarge = 0.65

	// collateralExponentiationSmall is the power to which we raise the weight
	// during collateral adjustment when the collateral is small. This large
	// number ensures a heavy focus on collateral when distinguishing between
	// hosts that have a very small amount of collateral provided compared to
	// the size of the allowance.
	//
	// The number is set relative to the price exponentiation, because the goal
	// is to ensure that the collateral has more weight than the price when the
	// collateral is small.
	collateralExponentiationSmall = priceExponentiation + 0.5

	// Set a minimum price, below which setting lower prices will no longer put
	// this host at an advatnage. This price is considered the bar for
	// 'essentially free', and is kept to a minimum to prevent certain Sybil
	// attack related attack vectors.
	//
	// NOTE: This needs to be intelligently adjusted down as the practical price
	// of storage changes, and as the price of the siacoin changes.
	minTotalPrice = types.SiacoinPrecision.Mul64(1).Div64(tbMonth)

	// priceDiveNormalization reduces the raw value of the price so that not so
	// many digits are needed when operating on the weight. This also allows the
	// base weight to be a lot lower.
	priceDivNormalization = types.SiacoinPrecision.Div64(100e3).Div64(tbMonth)

	// priceExponentiation is the number of times that the weight is divided by
	// the price.
	priceExponentiation = 5.0

	// requiredStorage indicates the amount of storage that the host must be
	// offering in order to be considered a valuable/worthwhile host.
	requiredStorage = build.Select(build.Var{
		Standard: uint64(20e9),
		Dev:      uint64(1e6),
		Testing:  uint64(1e3),
	}).(uint64)

	// tbMonth is the number of bytes in a terabyte times the number of blocks
	// in a month.
	tbMonth = uint64(4032) * uint64(1e12)
)

// collateralAdjustments improves the host's weight according to the amount of
// collateral that they have provided.
func (hdb *HostDB) collateralAdjustments(entry modules.HostDBEntry, allowance modules.Allowance, expectedStorage uint64) float64 {
	// Ensure that all values will avoid divide by zero errors.
	if allowance.Hosts == 0 {
		allowance.Hosts = 1
	}
	if allowance.Period == 0 {
		allowance.Period = 1
	}
	if expectedStorage == 0 {
		expectedStorage = 1
	}

	// Ensure that the allowance and expected storage will not brush up against
	// the max collateral. If the allowance comes within half of the max
	// collateral, cap the collateral that we use during adjustments based on
	// the max collateral instead of the per-byte collateral.
	hostCollateral := entry.Collateral
	requiredCollateral := entry.Collateral.Mul64(uint64(allowance.Period)).Mul64(expectedStorage)
	if requiredCollateral.Cmp(entry.MaxCollateral.Div64(2)) < 0 {
		hostCollateral = entry.MaxCollateral.Div64(uint64(allowance.Period)).Div64(expectedStorage).Div64(2)
	}

	// Determine the cutoff for the difference between small collateral and
	// large collateral. The cutoff is used to create a step function in the
	// collateral scoring where decreasing collateral results in much higher
	// penalties below a certain threshold.
	//
	// This threshold is attempting to be the threshold where the amount of
	// money becomes insignificant. A collateral that is 10x higher than the
	// price is not interesting, compelling, nor a sign of reliability if the
	// price and collateral are both effectively zero.
	cutoff := allowance.Funds.Div64(allowance.Hosts).Div64(uint64(allowance.Period)).Div64(expectedStorage).Div64(3)
	if cutoff.Cmp(hostCollateral) < 0 {
		// Set the cutoff equal to the collateral so that the ratio has a
		// minimum of 1, and also so that the smallWeight is computed based on
		// the actual collateral instead of just the cutoff.
		cutoff = hostCollateral
	}
	// Get the ratio between the cutoff and the actual collateral so we can
	// award the bonus for having a large collateral. Perform normalization
	// before converting to uint64.
	collateral64, _ := hostCollateral.Div(priceDivNormalization).Uint64()
	cutoff64, _ := cutoff.Div(priceDivNormalization).Uint64()
	if cutoff64 == 0 {
		cutoff64 = 1
	}
	ratio := float64(collateral64) / float64(cutoff64)

	// Use the cutoff to determine the score based on the small exponentiation
	// factor (which has a high exponentiation), and then use the ratio between
	// the two to determine the bonus gained from having a high collateral.
	smallWeight := math.Pow(float64(cutoff64), collateralExponentiationSmall)
	largeWeight := math.Pow(ratio, collateralExponentiationLarge)
	weight := smallWeight * largeWeight
	return weight
}

// interactionAdjustments determine the penalty to be applied to a host for the
// historic and currnet interactions with that host. This function focuses on
// historic interactions and ignores recent interactions.
func (hdb *HostDB) interactionAdjustments(entry modules.HostDBEntry) float64 {
	// Give the host a baseline of 30 successful interactions and 1 failed
	// interaction. This gives the host a baseline if we've had few
	// interactions with them. The 1 failed interaction will become
	// irrelevant after sufficient interactions with the host.
	hsi := entry.HistoricSuccessfulInteractions + 30
	hfi := entry.HistoricFailedInteractions + 1

	// Determine the intraction ratio based off of the historic interactions.
	ratio := float64(hsi) / float64(hsi+hfi)

	// Raise the ratio to the 15th power and return that. The exponentiation is
	// very high because the renter will already intentionally avoid hosts that
	// do not have many successful interactions, meaning that the bad points do
	// not rack up very quickly. We want to signal a bad score for the host
	// nonetheless.
	return math.Pow(ratio, 15)
}

// priceAdjustments will adjust the weight of the entry according to the prices
// that it has set.
func (hdb *HostDB) priceAdjustments(entry modules.HostDBEntry, allowance modules.Allowance, expectedStorage, expectedUploadFrequency, expectedDownloadFrequency uint64) float64 {
	// Sanity checks - the constants values need to have certain relationships
	// to eachother
	if build.DEBUG {
		// If the minTotalPrice is not much larger than the divNormalization,
		// there will be problems with granularity after the divNormalization is
		// applied.
		if minTotalPrice.Div64(1e3).Cmp(priceDivNormalization) < 0 {
			build.Critical("Maladjusted minDivePrice and divNormalization constants in hostdb package")
		}
	}

	// Prices tiered as follows:
	//    - the storage price is presented as 'per block per byte'
	//    - the contract price is presented as a flat rate
	//    - the upload bandwidth price is per byte
	//    - the download bandwidth price is per byte
	//
	// The hostdb will naively assume the following for now:
	//    - each contract covers 6 weeks of storage (default is 12 weeks, but
	//      renewals occur at midpoint) - 6048 blocks - and 25GB of storage.
	//    - uploads happen once per 12 weeks (average lifetime of a file is 12 weeks)
	//    - downloads happen once per 12 weeks (files are on average downloaded once throughout lifetime)
	//
	// In the future, the renter should be able to track average user behavior
	// and adjust accordingly. This flexibility will be added later.
	adjustedContractPrice := entry.ContractPrice.Div64(uint64(allowance.Period)).Div64(expectedStorage) // Adjust contract price to match 'expectedStorage' for 6 weeks.
	adjustedUploadPrice := entry.UploadBandwidthPrice.Div64(expectedUploadFrequency)                    // Adjust upload price to match a single upload over 24 weeks.
	adjustedDownloadPrice := entry.DownloadBandwidthPrice.Div64(expectedDownloadFrequency).Div64(3)     // Adjust download price to match one download over 12 weeks, 1 redundancy.
	totalPrice := entry.StoragePrice.Add(adjustedContractPrice).Add(adjustedUploadPrice).Add(adjustedDownloadPrice)

	// Set a minimum on the price, then normalize to a sane precision.
	if totalPrice.Cmp(minTotalPrice) < 0 {
		totalPrice = minTotalPrice
	}
	baseU64, err := minTotalPrice.Div(priceDivNormalization).Uint64()
	if err != nil {
		baseU64 = math.MaxUint64
	}
	actualU64, err := totalPrice.Div(priceDivNormalization).Uint64()
	if err != nil {
		actualU64 = math.MaxUint64
	}
	base := float64(baseU64)
	actual := float64(actualU64)

	return math.Pow(base/actual, priceExponentiation)
}

// storageRemainingAdjustments adjusts the weight of the entry according to how
// much storage it has remaining.
func storageRemainingAdjustments(entry modules.HostDBEntry) float64 {
	base := float64(1)
	if entry.RemainingStorage < 200*requiredStorage {
		base = base / 2 // 2x total penalty
	}
	if entry.RemainingStorage < 150*requiredStorage {
		base = base / 2 // 4x total penalty
	}
	if entry.RemainingStorage < 100*requiredStorage {
		base = base / 2 // 8x total penalty
	}
	if entry.RemainingStorage < 80*requiredStorage {
		base = base / 2 // 16x total penalty
	}
	if entry.RemainingStorage < 40*requiredStorage {
		base = base / 2 // 32x total penalty
	}
	if entry.RemainingStorage < 20*requiredStorage {
		base = base / 2 // 64x total penalty
	}
	if entry.RemainingStorage < 15*requiredStorage {
		base = base / 2 // 128x total penalty
	}
	if entry.RemainingStorage < 10*requiredStorage {
		base = base / 2 // 256x total penalty
	}
	if entry.RemainingStorage < 5*requiredStorage {
		base = base / 2 // 512x total penalty
	}
	if entry.RemainingStorage < 3*requiredStorage {
		base = base / 2 // 1024x total penalty
	}
	if entry.RemainingStorage < 2*requiredStorage {
		base = base / 2 // 2048x total penalty
	}
	if entry.RemainingStorage < requiredStorage {
		base = base / 2 // 4096x total penalty
	}
	return base
}

// versionAdjustments will adjust the weight of the entry according to the hsd
// version reported by the host.
func versionAdjustments(entry modules.HostDBEntry) float64 {
	base := float64(1)
	// NOTE: This could should be uncommented with appropiate version numbers
	// once we actually have enough code variation between versions that
	// it makes sense to penalize old hosts.
	/*
		if build.VersionCmp(entry.Version, "1.4.0") < 0 {
			base = base * 0.99999 // Safety value to make sure we update the version penalties every time we update the host.
		}
		if build.VersionCmp(entry.Version, "1.3.2") < 0 {
			base = base * 0.9
		}
		// we shouldn't use pre hardfork hosts
		if build.VersionCmp(entry.Version, "1.3.1") < 0 {
			base = math.SmallestNonzeroFloat64
		}
	*/
	return base
}

// lifetimeAdjustments will adjust the weight of the host according to the total
// amount of time that has passed since the host's original announcement.
func (hdb *HostDB) lifetimeAdjustments(entry modules.HostDBEntry) float64 {
	base := float64(1)
	if hdb.blockHeight >= entry.FirstSeen {
		age := hdb.blockHeight - entry.FirstSeen
		if age < 12000 {
			base = base * 2 / 3 // 1.5x total
		}
		if age < 6000 {
			base = base / 2 // 3x total
		}
		if age < 4000 {
			base = base / 2 // 6x total
		}
		if age < 2000 {
			base = base / 2 // 12x total
		}
		if age < 1000 {
			base = base / 3 // 36x total
		}
		if age < 576 {
			base = base / 3 // 108x total
		}
		if age < 288 {
			base = base / 3 // 324x total
		}
		if age < 144 {
			base = base / 3 // 972x total
		}
	}
	return base
}

// uptimeAdjustments penalizes the host for having poor uptime, and for being
// offline.
//
// CAUTION: The function 'updateEntry' will manually fill out two scans for a
// new host to give the host some initial uptime or downtime. Modification of
// this function needs to be made paying attention to the structure of that
// function.
func (hdb *HostDB) uptimeAdjustments(entry modules.HostDBEntry) float64 {
	// Special case: if we have scanned the host twice or fewer, don't perform
	// uptime math.
	if len(entry.ScanHistory) == 0 {
		return 0.25
	}
	if len(entry.ScanHistory) == 1 {
		if entry.ScanHistory[0].Success {
			return 0.75
		}
		return 0.25
	}
	if len(entry.ScanHistory) == 2 {
		if entry.ScanHistory[0].Success && entry.ScanHistory[1].Success {
			return 0.85
		}
		if entry.ScanHistory[0].Success || entry.ScanHistory[1].Success {
			return 0.50
		}
		return 0.05
	}

	// Compute the total measured uptime and total measured downtime for this
	// host.
	downtime := entry.HistoricDowntime
	uptime := entry.HistoricUptime
	recentTime := entry.ScanHistory[0].Timestamp
	recentSuccess := entry.ScanHistory[0].Success
	for _, scan := range entry.ScanHistory[1:] {
		if recentTime.After(scan.Timestamp) {
			if build.DEBUG {
				hdb.log.Critical("Host entry scan history not sorted.")
			} else {
				hdb.log.Print("WARNING: Host entry scan history not sorted.")
			}
			// Ignore the unsorted scan entry.
			continue
		}
		if recentSuccess {
			uptime += scan.Timestamp.Sub(recentTime)
		} else {
			downtime += scan.Timestamp.Sub(recentTime)
		}
		recentTime = scan.Timestamp
		recentSuccess = scan.Success
	}
	// Sanity check against 0 total time.
	if uptime == 0 && downtime == 0 {
		return 0.001 // Shouldn't happen.
	}

	// Compute the uptime ratio, but shift by 0.02 to acknowledge fully that
	// 98% uptime and 100% uptime is valued the same.
	uptimeRatio := float64(uptime) / float64(uptime+downtime)
	if uptimeRatio > 0.98 {
		uptimeRatio = 0.98
	}
	uptimeRatio += 0.02

	// Cap the total amount of downtime allowed based on the total number of
	// scans that have happened.
	allowedDowntime := 0.03 * float64(len(entry.ScanHistory))
	if uptimeRatio < 1-allowedDowntime {
		uptimeRatio = 1 - allowedDowntime
	}

	// Calculate the penalty for low uptime. Penalties increase extremely
	// quickly as uptime falls away from 95%.
	//
	// 100% uptime = 1
	// 98%  uptime = 1
	// 95%  uptime = 0.83
	// 90%  uptime = 0.26
	// 85%  uptime = 0.03
	// 80%  uptime = 0.001
	// 75%  uptime = 0.00001
	// 70%  uptime = 0.0000001
	exp := 200 * math.Min(1-uptimeRatio, 0.30)
	return math.Pow(uptimeRatio, exp)
}

// calculateHostWeightFn creates a hosttree.WeightFunc given an Allowance.
func (hdb *HostDB) calculateHostWeightFn(allowance modules.Allowance) hosttree.WeightFunc {
	// TODO: Pass these in as input instead of fixing them.
	//
	// expectedStorage is the amount of data that we expect to have in a
	// contract.
	//
	// expectedUploadFrequency is the expected number of blocks between each
	// complete re-upload of the filesystem. This will be a combination of the
	// rate at which a user uploads files, the rate at which a user replaces
	// files, and the rate at which a user has to repair files due to host
	// churn. If the expected storage is 25 GB and the expected upload frequency
	// is 24 weeks, it means the user is expected to do about 1 GB of upload per
	// week on average throughout the life of the contract.
	//
	// expectedDownloadFrequency is the expected number of blocks between each
	// complete download of the filesystem. This should include the user
	// downloading, streaming, and repairing files.
	expectedStorage := uint64(25e9)            // 25 GB - This contract is expected to store 25 GB.
	expectedUploadFrequency := uint64(24192)   // 24 weeks.
	expectedDownloadFrequency := uint64(12096) // 12 weeks.

	return func(entry modules.HostDBEntry) types.Currency {
		collateralReward := hdb.collateralAdjustments(entry, allowance, expectedStorage)
		interactionPenalty := hdb.interactionAdjustments(entry)
		lifetimePenalty := hdb.lifetimeAdjustments(entry)
		pricePenalty := hdb.priceAdjustments(entry, allowance, expectedStorage, expectedUploadFrequency, expectedDownloadFrequency)
		storageRemainingPenalty := storageRemainingAdjustments(entry)
		uptimePenalty := hdb.uptimeAdjustments(entry)
		versionPenalty := versionAdjustments(entry)

		// Combine the adjustments.
		fullPenalty := collateralReward * interactionPenalty * lifetimePenalty *
			pricePenalty * storageRemainingPenalty * uptimePenalty * versionPenalty

		// Return a types.Currency.
		weight := baseWeight.MulFloat(fullPenalty)
		if weight.IsZero() {
			// A weight of zero is problematic for for the host tree.
			return types.NewCurrency64(1)
		}
		return weight
	}
}

// calculateConversionRate calculates the conversion rate of the provided
// host score, comparing it to the hosts in the database and returning what
// percentage of contracts it is likely to participate in.
func (hdb *HostDB) calculateConversionRate(score types.Currency) float64 {
	var totalScore types.Currency
	for _, h := range hdb.ActiveHosts() {
		totalScore = totalScore.Add(hdb.weightFunc(h))
	}
	if totalScore.IsZero() {
		totalScore = types.NewCurrency64(1)
	}
	conversionRate, _ := big.NewRat(0, 1).SetFrac(score.Mul64(50).Big(), totalScore.Big()).Float64()
	if conversionRate > 100 {
		conversionRate = 100
	}
	return conversionRate
}

// EstimateHostScore takes a HostExternalSettings and returns the estimated
// score of that host in the hostdb, assuming no penalties for age or uptime.
func (hdb *HostDB) EstimateHostScore(entry modules.HostDBEntry, allowance modules.Allowance) modules.HostScoreBreakdown {
	// TODO: Pass these in as input instead of fixing them.
	//
	// expectedStorage is the amount of data that we expect to have in a
	// contract.
	//
	// expectedUploadFrequency is the expected number of blocks between each
	// complete re-upload of the filesystem. This will be a combination of the
	// rate at which a user uploads files, the rate at which a user replaces
	// files, and the rate at which a user has to repair files due to host
	// churn. If the expected storage is 25 GB and the expected upload frequency
	// is 24 weeks, it means the user is expected to do about 1 GB of upload per
	// week on average throughout the life of the contract.
	//
	// expectedDownloadFrequency is the expected number of blocks between each
	// complete download of the filesystem. This should include the user
	// downloading, streaming, and repairing files.
	expectedStorage := uint64(25e9)            // 25 GB - This contract is expected to store 25 GB.
	expectedUploadFrequency := uint64(24192)   // 24 weeks.
	expectedDownloadFrequency := uint64(12096) // 12 weeks.

	// Grab the adjustments. Age, and uptime penalties are set to '1', to
	// assume best behavior from the host.
	collateralReward := hdb.collateralAdjustments(entry, allowance, expectedStorage)
	pricePenalty := hdb.priceAdjustments(entry, allowance, expectedStorage, expectedUploadFrequency, expectedDownloadFrequency)
	storageRemainingPenalty := storageRemainingAdjustments(entry)
	versionPenalty := versionAdjustments(entry)

	// Combine into a full penalty, then determine the resulting estimated
	// score.
	fullPenalty := collateralReward * pricePenalty * storageRemainingPenalty * versionPenalty
	estimatedScore := baseWeight.MulFloat(fullPenalty)
	if estimatedScore.IsZero() {
		estimatedScore = types.NewCurrency64(1)
	}

	// Compile the estimates into a host score breakdown.
	return modules.HostScoreBreakdown{
		Score:          estimatedScore,
		ConversionRate: hdb.calculateConversionRate(estimatedScore),

		AgeAdjustment:              1,
		BurnAdjustment:             1,
		CollateralAdjustment:       collateralReward,
		PriceAdjustment:            pricePenalty,
		StorageRemainingAdjustment: storageRemainingPenalty,
		UptimeAdjustment:           1,
		VersionAdjustment:          versionPenalty,
	}
}

// ScoreBreakdown provdes a detailed set of scalars and bools indicating
// elements of the host's overall score.
func (hdb *HostDB) ScoreBreakdown(entry modules.HostDBEntry) modules.HostScoreBreakdown {
	// TODO: Pass these in as input instead of fixing them.
	//
	// expectedStorage is the amount of data that we expect to have in a
	// contract.
	//
	// expectedUploadFrequency is the expected number of blocks between each
	// complete re-upload of the filesystem. This will be a combination of the
	// rate at which a user uploads files, the rate at which a user replaces
	// files, and the rate at which a user has to repair files due to host
	// churn. If the expected storage is 25 GB and the expected upload frequency
	// is 24 weeks, it means the user is expected to do about 1 GB of upload per
	// week on average throughout the life of the contract.
	//
	// expectedDownloadFrequency is the expected number of blocks between each
	// complete download of the filesystem. This should include the user
	// downloading, streaming, and repairing files.
	expectedStorage := uint64(25e9)            // 25 GB - This contract is expected to store 25 GB.
	expectedUploadFrequency := uint64(24192)   // 24 weeks.
	expectedDownloadFrequency := uint64(12096) // 12 weeks.

	hdb.mu.Lock()
	defer hdb.mu.Unlock()

	score := hdb.weightFunc(entry)
	return modules.HostScoreBreakdown{
		Score:          score,
		ConversionRate: hdb.calculateConversionRate(score),

		AgeAdjustment:              hdb.lifetimeAdjustments(entry),
		BurnAdjustment:             1,
		CollateralAdjustment:       hdb.collateralAdjustments(entry, hdb.allowance, expectedStorage),
		InteractionAdjustment:      hdb.interactionAdjustments(entry),
		PriceAdjustment:            hdb.priceAdjustments(entry, hdb.allowance, expectedStorage, expectedUploadFrequency, expectedDownloadFrequency),
		StorageRemainingAdjustment: storageRemainingAdjustments(entry),
		UptimeAdjustment:           hdb.uptimeAdjustments(entry),
		VersionAdjustment:          versionAdjustments(entry),
	}
}
