package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/HyperspaceApp/Hyperspace/crypto"
	"github.com/HyperspaceApp/Hyperspace/modules"
	"github.com/HyperspaceApp/Hyperspace/types"

	"github.com/HyperspaceApp/entropy-mnemonics"
	"github.com/julienschmidt/httprouter"
)

type (
	// WalletGET contains general information about the wallet.
	WalletGET struct {
		Encrypted  bool              `json:"encrypted"`
		Height     types.BlockHeight `json:"height"`
		Rescanning bool              `json:"rescanning"`
		Unlocked   bool              `json:"unlocked"`

		ConfirmedSiacoinBalance     types.Currency `json:"confirmedspacecashbalance"`
		UnconfirmedOutgoingSiacoins types.Currency `json:"unconfirmedoutgoingspacecash"`
		UnconfirmedIncomingSiacoins types.Currency `json:"unconfirmedincomingspacecash"`

		SiacoinClaimBalance types.Currency `json:"siacoinclaimbalance"`

		DustThreshold types.Currency `json:"dustthreshold"`
	}

	// WalletAddressGET contains an address returned by a GET call to
	// /wallet/address.
	WalletAddressGET struct {
		Address types.UnlockHash `json:"address"`
	}

	// WalletAddressPOST contains an address returned by a POST call to
	// /wallet/address.
	WalletAddressPOST struct {
		Address types.UnlockHash `json:"address"`
	}

	// WalletAddressesGET contains the list of wallet addresses returned by a
	// GET call to /wallet/addresses.
	WalletAddressesGET struct {
		Addresses []types.UnlockHash `json:"addresses"`
	}

	// WalletInitPOST contains the primary seed that gets generated during a
	// POST call to /wallet/init.
	WalletInitPOST struct {
		PrimarySeed string `json:"primaryseed"`
	}

	// WalletSiacoinsPOST contains the transaction sent in the POST call to
	// /wallet/spacecash.
	WalletSiacoinsPOST struct {
		TransactionIDs []types.TransactionID `json:"transactionids"`
	}

	// WalletSignPOSTParams contains the unsigned transaction and a set of
	// inputs to sign.
	WalletSignPOSTParams struct {
		Transaction types.Transaction `json:"transaction"`
		ToSign      []crypto.Hash     `json:"tosign"`
	}

	// WalletSignPOSTResp contains the signed transaction.
	WalletSignPOSTResp struct {
		Transaction types.Transaction `json:"transaction"`
	}

	// WalletSeedsGET contains the seeds used by the wallet.
	WalletSeedsGET struct {
		PrimarySeed        string   `json:"primaryseed"`
		AddressesRemaining int      `json:"addressesremaining"`
		AllSeeds           []string `json:"allseeds"`
	}

	// WalletSweepPOST contains the coins and funds returned by a call to
	// /wallet/sweep.
	WalletSweepPOST struct {
		Coins types.Currency `json:"coins"`
		Funds types.Currency `json:"funds"`
	}

	// WalletTransactionGETid contains the transaction returned by a call to
	// /wallet/transaction/:id
	WalletTransactionGETid struct {
		Transaction modules.ProcessedTransaction `json:"transaction"`
	}

	// WalletBuildTransactionGET contains the transaction returned by a call to
	// /wallet/build/transaction
	WalletBuildTransactionGET struct {
		Transaction types.Transaction `json:"transaction"`
	}

	// WalletTransactionsGET contains the specified set of confirmed and
	// unconfirmed transactions.
	WalletTransactionsGET struct {
		ConfirmedTransactions   []modules.ProcessedTransaction `json:"confirmedtransactions"`
		UnconfirmedTransactions []modules.ProcessedTransaction `json:"unconfirmedtransactions"`
	}

	// WalletTransactionsGETaddr contains the set of wallet transactions
	// relevant to the input address provided in the call to
	// /wallet/transaction/:addr
	WalletTransactionsGETaddr struct {
		ConfirmedTransactions   []modules.ProcessedTransaction `json:"confirmedtransactions"`
		UnconfirmedTransactions []modules.ProcessedTransaction `json:"unconfirmedtransactions"`
	}

	// WalletUnlockConditionsGET contains a set of unlock conditions.
	WalletUnlockConditionsGET struct {
		UnlockConditions types.UnlockConditions `json:"unlockconditions"`
	}

	// WalletUnlockConditionsPOSTParams contains a set of unlock conditions.
	WalletUnlockConditionsPOSTParams struct {
		UnlockConditions types.UnlockConditions `json:"unlockconditions"`
	}

	// WalletUnspentGET contains the unspent outputs tracked by the wallet.
	// The MaturityHeight field of each output indicates the height of the
	// block that the output appeared in.
	WalletUnspentGET struct {
		Outputs []modules.UnspentOutput `json:"outputs"`
	}

	// WalletVerifyAddressGET contains a bool indicating if the address passed to
	// /wallet/verify/address/:addr is a valid address.
	WalletVerifyAddressGET struct {
		Valid bool `json:"valid"`
	}

	// WalletWatchPOST contains the set of addresses to add or remove from the
	// watch set.
	WalletWatchPOST struct {
		Addresses []types.UnlockHash `json:"addresses"`
		Remove    bool               `json:"remove"`
		Unused    bool               `json:"unused"`
	}

	// WalletWatchGET contains the set of addresses that the wallet is
	// currently watching.
	WalletWatchGET struct {
		Addresses []types.UnlockHash `json:"addresses"`
	}
)

// encryptionKeys enumerates the possible encryption keys that can be derived
// from an input string.
func encryptionKeys(seedStr string) (validKeys []crypto.CipherKey) {
	dicts := []mnemonics.DictionaryID{"english", "german", "japanese"}
	for _, dict := range dicts {
		seed, err := modules.StringToSeed(seedStr, dict)
		if err != nil {
			continue
		}
		validKeys = append(validKeys, crypto.NewWalletKey(crypto.HashObject(seed)))
	}
	validKeys = append(validKeys, crypto.NewWalletKey(crypto.HashObject(seedStr)))
	return validKeys
}

// walletHander handles API calls to /wallet.
func (api *API) walletHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	siacoinBal, err := api.wallet.ConfirmedBalance()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	siacoinsOut, siacoinsIn, err := api.wallet.UnconfirmedBalance()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	dustThreshold, err := api.wallet.DustThreshold()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	encrypted, err := api.wallet.Encrypted()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	unlocked, err := api.wallet.Unlocked()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	rescanning, err := api.wallet.Rescanning()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	height, err := api.wallet.Height()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletGET{
		Encrypted:  encrypted,
		Unlocked:   unlocked,
		Rescanning: rescanning,
		Height:     height,

		ConfirmedSiacoinBalance:     siacoinBal,
		UnconfirmedOutgoingSiacoins: siacoinsOut,
		UnconfirmedIncomingSiacoins: siacoinsIn,

		DustThreshold: dustThreshold,
	})
}

// walletGetAddressHandler handles GET API calls to /wallet/address.
func (api *API) walletGetAddressHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	unlockConditions, err := api.wallet.GetAddress()
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/addresses: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletAddressGET{
		Address: unlockConditions.UnlockHash(),
	})
}

// walletCreateAddressHandler handles POST API calls to /wallet/address.
func (api *API) walletCreateAddressHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	unlockConditions, err := api.wallet.NextAddress()
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/addresses: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletAddressPOST{
		Address: unlockConditions.UnlockHash(),
	})
}

// walletAddressHandler handles API calls to /wallet/addresses.
func (api *API) walletAddressesHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	addresses, err := api.wallet.AllAddresses()
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("Error when calling /wallet/addresses: %v", err)}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletAddressesGET{
		Addresses: addresses,
	})
}

// walletBackupHandler handles API calls to /wallet/backup.
func (api *API) walletBackupHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	destination := req.FormValue("destination")
	// Check that the destination is absolute.
	if !filepath.IsAbs(destination) {
		WriteError(w, Error{"error when calling /wallet/backup: destination must be an absolute path"}, http.StatusBadRequest)
		return
	}
	err := api.wallet.CreateBackup(destination)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// walletInitHandler handles API calls to /wallet/init.
func (api *API) walletInitHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var encryptionKey crypto.CipherKey
	if req.FormValue("encryptionpassword") != "" {
		encryptionKey = crypto.NewWalletKey(crypto.HashObject(req.FormValue("encryptionpassword")))
	}

	if req.FormValue("force") == "true" {
		err := api.wallet.Reset()
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/init: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	seed, err := api.wallet.Encrypt(encryptionKey)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/init: " + err.Error()}, http.StatusBadRequest)
		return
	}

	dictID := mnemonics.DictionaryID(req.FormValue("dictionary"))
	if dictID == "" {
		dictID = "english"
	}
	seedStr, err := modules.SeedToString(seed, dictID)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/init: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletInitPOST{
		PrimarySeed: seedStr,
	})
}

// walletInitSeedHandler handles API calls to /wallet/init/seed.
func (api *API) walletInitSeedHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var encryptionKey crypto.CipherKey
	if req.FormValue("encryptionpassword") != "" {
		encryptionKey = crypto.NewWalletKey(crypto.HashObject(req.FormValue("encryptionpassword")))
	}
	dictID := mnemonics.DictionaryID(req.FormValue("dictionary"))
	if dictID == "" {
		dictID = "english"
	}
	seed, err := modules.StringToSeed(req.FormValue("seed"), dictID)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/init/seed: " + err.Error()}, http.StatusBadRequest)
		return
	}

	if req.FormValue("force") == "true" {
		err = api.wallet.Reset()
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/init/seed: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	err = api.wallet.InitFromSeed(encryptionKey, seed)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/init/seed: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// walletSeedHandler handles API calls to /wallet/seed.
func (api *API) walletSeedHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Get the seed using the ditionary + phrase
	dictID := mnemonics.DictionaryID(req.FormValue("dictionary"))
	if dictID == "" {
		dictID = "english"
	}
	seed, err := modules.StringToSeed(req.FormValue("seed"), dictID)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/seed: " + err.Error()}, http.StatusBadRequest)
		return
	}

	potentialKeys := encryptionKeys(req.FormValue("encryptionpassword"))
	for _, key := range potentialKeys {
		err := api.wallet.LoadSeed(key, seed)
		if err == nil {
			WriteSuccess(w)
			return
		}
		if err != nil && err != modules.ErrBadEncryptionKey {
			WriteError(w, Error{"error when calling /wallet/seed: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	WriteError(w, Error{"error when calling /wallet/seed: " + modules.ErrBadEncryptionKey.Error()}, http.StatusBadRequest)
}

// walletSiagkeyHandler handles API calls to /wallet/siagkey.
func (api *API) walletSiagkeyHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Fetch the list of keyfiles from the post body.
	keyfiles := strings.Split(req.FormValue("keyfiles"), ",")
	potentialKeys := encryptionKeys(req.FormValue("encryptionpassword"))

	for _, keypath := range keyfiles {
		// Check that all key paths are absolute paths.
		if !filepath.IsAbs(keypath) {
			WriteError(w, Error{"error when calling /wallet/siagkey: keyfiles contains a non-absolute path"}, http.StatusBadRequest)
			return
		}
	}

	for _, key := range potentialKeys {
		err := api.wallet.LoadSiagKeys(key, keyfiles)
		if err == nil {
			WriteSuccess(w)
			return
		}
		if err != nil && err != modules.ErrBadEncryptionKey {
			WriteError(w, Error{"error when calling /wallet/siagkey: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	WriteError(w, Error{"error when calling /wallet/siagkey: " + modules.ErrBadEncryptionKey.Error()}, http.StatusBadRequest)
}

// walletLockHanlder handles API calls to /wallet/lock.
func (api *API) walletLockHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	err := api.wallet.Lock()
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// walletSeedsHandler handles API calls to /wallet/seeds.
func (api *API) walletSeedsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	dictionary := mnemonics.DictionaryID(req.FormValue("dictionary"))
	if dictionary == "" {
		dictionary = mnemonics.English
	}

	// Get the primary seed information.
	primarySeed, addrsRemaining, err := api.wallet.PrimarySeed()
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/seeds: " + err.Error()}, http.StatusBadRequest)
		return
	}
	primarySeedStr, err := modules.SeedToString(primarySeed, dictionary)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/seeds: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Get the list of seeds known to the wallet.
	allSeeds, err := api.wallet.AllSeeds()
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/seeds: " + err.Error()}, http.StatusBadRequest)
		return
	}
	var allSeedsStrs []string
	for _, seed := range allSeeds {
		str, err := modules.SeedToString(seed, dictionary)
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/seeds: " + err.Error()}, http.StatusBadRequest)
			return
		}
		allSeedsStrs = append(allSeedsStrs, str)
	}
	WriteJSON(w, WalletSeedsGET{
		PrimarySeed:        primarySeedStr,
		AddressesRemaining: int(addrsRemaining),
		AllSeeds:           allSeedsStrs,
	})
}

// walletSiacoinsHandler handles API calls to /wallet/spacecash.
func (api *API) walletSiacoinsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var txns []types.Transaction
	if req.FormValue("outputs") != "" {
		// multiple amounts + destinations
		if req.FormValue("amount") != "" || req.FormValue("destination") != "" {
			WriteError(w, Error{"cannot supply both 'outputs' and single amount+destination pair"}, http.StatusInternalServerError)
			return
		}

		var outputs []types.SiacoinOutput
		err := json.Unmarshal([]byte(req.FormValue("outputs")), &outputs)
		if err != nil {
			WriteError(w, Error{"could not decode outputs: " + err.Error()}, http.StatusInternalServerError)
			return
		}
		txns, err = api.wallet.SendSiacoinsMulti(outputs)
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/spacecash: " + err.Error()}, http.StatusInternalServerError)
			return
		}
	} else {
		// single amount + destination
		amount, ok := scanAmount(req.FormValue("amount"))
		if !ok {
			WriteError(w, Error{"could not read amount from POST call to /wallet/spacecash"}, http.StatusBadRequest)
			return
		}
		dest, err := scanAddress(req.FormValue("destination"))
		if err != nil {
			WriteError(w, Error{"could not read address from POST call to /wallet/spacecash"}, http.StatusBadRequest)
			return
		}

		txns, err = api.wallet.SendSiacoins(amount, dest)
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/spacecash: " + err.Error()}, http.StatusInternalServerError)
			return
		}

	}

	var txids []types.TransactionID
	for _, txn := range txns {
		txids = append(txids, txn.ID())
	}
	WriteJSON(w, WalletSiacoinsPOST{
		TransactionIDs: txids,
	})
}

// walletSweepSeedHandler handles API calls to /wallet/sweep/seed.
func (api *API) walletSweepSeedHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Get the seed using the ditionary + phrase
	dictID := mnemonics.DictionaryID(req.FormValue("dictionary"))
	if dictID == "" {
		dictID = "english"
	}
	seed, err := modules.StringToSeed(req.FormValue("seed"), dictID)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/sweep/seed: " + err.Error()}, http.StatusBadRequest)
		return
	}

	coins, funds, err := api.wallet.SweepSeed(seed)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/sweep/seed: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletSweepPOST{
		Coins: coins,
		Funds: funds,
	})
}

// walletTransactionHandler handles API calls to /wallet/transaction/:id.
func (api *API) walletTransactionHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse the id from the url.
	var id types.TransactionID
	jsonID := "\"" + ps.ByName("id") + "\""
	err := id.UnmarshalJSON([]byte(jsonID))
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/transaction/id:" + err.Error()}, http.StatusBadRequest)
		return
	}

	txn, ok, err := api.wallet.Transaction(id)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/transaction/id:" + err.Error()}, http.StatusBadRequest)
		return
	}
	if !ok {
		WriteError(w, Error{"error when calling /wallet/transaction/:id  :  transaction not found"}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletTransactionGETid{
		Transaction: txn,
	})
}

// walletTransactionsHandler handles API calls to /wallet/transactions.
func (api *API) walletTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	startheightStr, endheightStr, depthStr := req.FormValue("startheight"), req.FormValue("endheight"), req.FormValue("depth")
	countStr, watchOnlyStr, categoryStr := req.FormValue("count"), req.FormValue("watchonly"), req.FormValue("category")
	var start, end, depth uint64
	var watchOnly bool
	var count uint64
	var category string
	var err error
	var confirmedTxns, unconfirmedTxns []modules.ProcessedTransaction
	// handle depth, startheight, endheight searches
	if depthStr != "" || startheightStr != "" || endheightStr != "" {
		if watchOnlyStr != "" || countStr != "" || categoryStr != "" {
			WriteError(w, Error{"startheight, endheight, and depth are incompatible with watchonly, count, and category."}, http.StatusBadRequest)
		}
		if depthStr == "" {
			if startheightStr == "" || endheightStr == "" {
				WriteError(w, Error{"startheight and endheight must be provided to a /wallet/transactions call if depth is unspecified."}, http.StatusBadRequest)
				return
			}
			// Get the start and end blocks.
			start, err = strconv.ParseUint(startheightStr, 10, 64)
			if err != nil {
				WriteError(w, Error{"parsing integer value for parameter `startheight` failed: " + err.Error()}, http.StatusBadRequest)
				return
			}
			// Check if endheightStr is set to -1. If it is, we use MaxUint64 as the
			// end. Otherwise we parse the argument as an unsigned integer.
			if endheightStr == "-1" {
				end = math.MaxUint64
			} else {
				end, err = strconv.ParseUint(endheightStr, 10, 64)
			}
			if err != nil {
				WriteError(w, Error{"parsing integer value for parameter `endheight` failed: " + err.Error()}, http.StatusBadRequest)
				return
			}
		} else {
			if startheightStr != "" || endheightStr != "" {
				WriteError(w, Error{"startheight and endheight must not be provided to a /wallet/transactions call if depth is specified."}, http.StatusBadRequest)
				return
			}
			// Get the start and end blocks by looking backwards from our current height.
			depth, err = strconv.ParseUint(depthStr, 10, 64)
			if err != nil {
				WriteError(w, Error{"parsing integer value for parameter `depth` failed: " + err.Error()}, http.StatusBadRequest)
				return
			}
			height, err := api.wallet.Height()
			if err != nil {
				WriteError(w, Error{fmt.Sprintf("Error when calling /wallet: %v", err)}, http.StatusBadRequest)
				return
			}
			end = uint64(height)
			start = end - depth - 1
			if start < 0 {
				start = 0
			}
		}
		confirmedTxns, err = api.wallet.Transactions(types.BlockHeight(start), types.BlockHeight(end))
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/transactions: " + err.Error()}, http.StatusBadRequest)
			return
		}
		unconfirmedTxns, err = api.wallet.UnconfirmedTransactions()
		if err != nil {
			WriteError(w, Error{"error when calling /wallet/transactions: " + err.Error()}, http.StatusBadRequest)
			return
		}
		// handle count, watchonly, category searches
	} else {
		count = 10
		if countStr != "" {
			count, err = strconv.ParseUint(countStr, 10, 64)
			if err != nil {
				WriteError(w, Error{"parsing integer value for parameter `count` failed: " + err.Error()}, http.StatusBadRequest)
				return
			}
		}
		watchOnly = false
		if watchOnlyStr != "" {
			watchOnly, err = strconv.ParseBool(watchOnlyStr)
			if err != nil {
				WriteError(w, Error{"parsing integer value for parameter `watchonly` failed: " + err.Error()}, http.StatusBadRequest)
				return
			}
		}
		category = ""
		if categoryStr != "" {
			if categoryStr != "send" && categoryStr != "receive" {
				if err != nil {
					WriteError(w, Error{"parameter `category` only accepts `send` or `receive` as values"}, http.StatusBadRequest)
					return
				}
			} else {
				category = categoryStr
			}
		}
		confirmedTxns, err = api.wallet.FilteredTransactions(count, watchOnly, category)
		unconfirmedTxns, err = api.wallet.FilteredUnconfirmedTransactions(watchOnly, category)
	}

	WriteJSON(w, WalletTransactionsGET{
		ConfirmedTransactions:   confirmedTxns,
		UnconfirmedTransactions: unconfirmedTxns,
	})
}

// walletTransactionsAddrHandler handles API calls to
// /wallet/transactions/:addr.
func (api *API) walletTransactionsAddrHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse the address being input.
	jsonAddr := "\"" + ps.ByName("addr") + "\""
	var addr types.UnlockHash
	err := addr.UnmarshalJSON([]byte(jsonAddr))
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/transactions: " + err.Error()}, http.StatusBadRequest)
		return
	}

	confirmedATs, err := api.wallet.AddressTransactions(addr)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/transactions: " + err.Error()}, http.StatusBadRequest)
		return
	}
	unconfirmedATs, err := api.wallet.AddressUnconfirmedTransactions(addr)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/transactions: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletTransactionsGETaddr{
		ConfirmedTransactions:   confirmedATs,
		UnconfirmedTransactions: unconfirmedATs,
	})
}

// walletBuildTransactionHandler handles API calls to
// /wallet/transactions/build.
func (api *API) walletBuildTransactionHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// single amount + destination
	amount, ok := scanAmount(req.FormValue("amount"))
	if !ok {
		WriteError(w, Error{"could not read amount from GET call to /wallet/build/transaction"}, http.StatusBadRequest)
		return
	}
	dest, err := scanAddress(req.FormValue("destination"))
	if err != nil {
		WriteError(w, Error{"could not read address from GET call to /wallet/build/transaction"}, http.StatusBadRequest)
		return
	}
	var fee types.Currency

	txnSet, err := api.wallet.NewTransactionSetForAddress(dest, amount, fee)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/build/transaction:" + err.Error()}, http.StatusBadRequest)
		return
	}

	if len(txnSet) > 1 {
		WriteError(w, Error{"error when calling /wallet/build/transaction: could not fit the desired amount into a single transaction, try sending a smaller amount"}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, WalletBuildTransactionGET{
		Transaction: txnSet[0],
	})
}

// walletUnlockHandler handles API calls to /wallet/unlock.
func (api *API) walletUnlockHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	potentialKeys := encryptionKeys(req.FormValue("encryptionpassword"))
	for _, key := range potentialKeys {
		err := api.wallet.Unlock(key)
		if err == nil {
			WriteSuccess(w)
			return
		}
		if err != nil && err != modules.ErrBadEncryptionKey {
			WriteError(w, Error{"error when calling /wallet/unlock: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	WriteError(w, Error{"error when calling /wallet/unlock: " + modules.ErrBadEncryptionKey.Error()}, http.StatusBadRequest)
}

// walletChangePasswordHandler handles API calls to /wallet/changepassword
func (api *API) walletChangePasswordHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var newKey crypto.CipherKey
	newPassword := req.FormValue("newpassword")
	if newPassword == "" {
		WriteError(w, Error{"a password must be provided to newpassword"}, http.StatusBadRequest)
		return
	}
	newKey = crypto.NewWalletKey(crypto.HashObject(newPassword))

	originalKeys := encryptionKeys(req.FormValue("encryptionpassword"))
	for _, key := range originalKeys {
		err := api.wallet.ChangeKey(key, newKey)
		if err == nil {
			WriteSuccess(w)
			return
		}
		if err != nil && err != modules.ErrBadEncryptionKey {
			WriteError(w, Error{"error when calling /wallet/changepassword: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	WriteError(w, Error{"error when calling /wallet/changepassword: " + modules.ErrBadEncryptionKey.Error()}, http.StatusBadRequest)
}

// walletVerifyAddressHandler handles API calls to /wallet/verify/address/:addr.
func (api *API) walletVerifyAddressHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	addrString := ps.ByName("addr")

	err := new(types.UnlockHash).LoadString(addrString)
	WriteJSON(w, WalletVerifyAddressGET{Valid: err == nil})
}

// walletUnlockConditionsHandlerGET handles GET calls to /wallet/unlockconditions.
func (api *API) walletUnlockConditionsHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	var addr types.UnlockHash
	err := addr.LoadString(ps.ByName("addr"))
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/unlockconditions: " + err.Error()}, http.StatusBadRequest)
		return
	}
	uc, err := api.wallet.UnlockConditions(addr)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/unlockconditions: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletUnlockConditionsGET{
		UnlockConditions: uc,
	})
}

// walletUnlockConditionsHandlerPOST handles POST calls to /wallet/unlockconditions.
func (api *API) walletUnlockConditionsHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	var params WalletUnlockConditionsPOSTParams
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}
	err = api.wallet.AddUnlockConditions(params.UnlockConditions)
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/unlockconditions: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// walletUnspentHandler handles API calls to /wallet/unspent.
func (api *API) walletUnspentHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	outputs, err := api.wallet.UnspentOutputs()
	if err != nil {
		WriteError(w, Error{"error when calling /wallet/unspent: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, WalletUnspentGET{
		Outputs: outputs,
	})
}

// walletSignHandler handles API calls to /wallet/sign.
func (api *API) walletSignHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var params WalletSignPOSTParams
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}
	err = api.wallet.SignTransaction(&params.Transaction, params.ToSign)
	if err != nil {
		WriteError(w, Error{"failed to sign transaction: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletSignPOSTResp{
		Transaction: params.Transaction,
	})
}

// walletWatchHandlerGET handles GET calls to /wallet/watch.
func (api *API) walletWatchHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	addrs, err := api.wallet.WatchAddresses()
	if err != nil {
		WriteError(w, Error{"failed to get watch addresses: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, WalletWatchGET{
		Addresses: addrs,
	})
}

// walletWatchHandlerPOST handles POST calls to /wallet/watch.
func (api *API) walletWatchHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var wwpp WalletWatchPOST
	err := json.NewDecoder(req.Body).Decode(&wwpp)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}
	if wwpp.Remove {
		err = api.wallet.RemoveWatchAddresses(wwpp.Addresses, wwpp.Unused)
	} else {
		err = api.wallet.AddWatchAddresses(wwpp.Addresses, wwpp.Unused)
	}
	if err != nil {
		WriteError(w, Error{"failed to update watch set: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
