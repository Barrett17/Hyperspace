package client

import (
	"net/url"
	"strconv"

	"github.com/HardDriveCoin/HardDriveCoin/modules"
	"github.com/HardDriveCoin/HardDriveCoin/node/api"
)

// RenterContractsGet requests the /renter/contracts resource
func (c *Client) RenterContractsGet() (rc api.RenterContracts, err error) {
	err = c.Get("/renter/contracts", &rc)
	return
}

// RenterPost uses the /renter endpoint to change the renter's allowance
func (c *Client) RenterPost(allowance modules.Allowance) (err error) {
	values := url.Values{}
	values.Set("funds", allowance.Funds.String())
	values.Set("hosts", strconv.FormatUint(allowance.Hosts, 10))
	values.Set("period", strconv.FormatUint(uint64(allowance.Period), 10))
	values.Set("renewwindow", strconv.FormatUint(uint64(allowance.RenewWindow), 10))
	err = c.Post("/renter", values.Encode(), nil)
	return
}
