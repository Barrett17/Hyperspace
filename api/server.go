package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/NebulousLabs/Sia/build"
	"github.com/NebulousLabs/Sia/modules"
)

// A Server is essentially a collection of modules and an API server to talk
// to them all.
type Server struct {
	api *API

	apiServer         *http.Server
	listener          net.Listener
	requiredUserAgent string

	// wg is used to block Close() from returning until Serve() has finished. A
	// WaitGroup is used instead of a chan struct{} so that Close() can be called
	// without necessarily calling Serve() first.
	wg sync.WaitGroup
}

// NewServer creates a new API server from the provided modules. The API will
// require authentication using HTTP basic auth if the supplied password is not
// the empty string. Usernames are ignored for authentication. This type of
// authentication sends passwords in plaintext and should therefore only be
// used if the APIaddr is localhost.
func NewServer(APIaddr string, requiredUserAgent string, requiredPassword string, cs modules.ConsensusSet, e modules.Explorer, g modules.Gateway, h modules.Host, m modules.Miner, r modules.Renter, tp modules.TransactionPool, w modules.Wallet) (*Server, error) {
	l, err := net.Listen("tcp", APIaddr)
	if err != nil {
		return nil, err
	}

	a := NewAPI(requiredUserAgent, requiredPassword, cs, e, g, h, m, r, tp, w)
	serv := &http.Server{
		Handler: a.Handler,
	}
	srv := &Server{
		api: a,

		listener:          l,
		requiredUserAgent: requiredUserAgent,
		apiServer:         serv,
	}

	return srv, nil
}

// Serve listens for and handles API calls. It is a blocking function.
func (srv *Server) Serve() error {
	// Block the Close() method until Serve() has finished.
	srv.wg.Add(1)
	defer srv.wg.Done()

	// stop the server if a kill signal is caught
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, os.Kill)
	defer signal.Stop(sigChan)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-sigChan:
			fmt.Println("\rCaught stop signal, quitting...")
			srv.Close()
		case <-stop:
			// Don't leave a dangling goroutine.
		}
	}()

	// The server will run until an error is encountered or the listener is
	// closed, via either the Close method or the signal handling above.
	// Closing the listener will result in the benign error handled below.
	err := srv.apiServer.Serve(srv.listener)
	if err != nil && !strings.HasSuffix(err.Error(), "use of closed network connection") {
		return err
	}
	return nil
}

// Close closes the Server's listener, causing the HTTP server to shut down.
func (srv *Server) Close() error {
	var errs []error

	// Close the listener, which will cause Server.Serve() to return.
	if err := srv.listener.Close(); err != nil {
		errs = append(errs, fmt.Errorf("listener.Close failed: %v", err))
	}

	// Wait for Server.Serve() to exit. We wait so that it's guaranteed that the
	// server has completely closed after Close() returns. This is particularly
	// useful during testing so that we don't exit a test before Serve() finishes.
	srv.wg.Wait()

	// Safely close each module.
	mods := []struct {
		name string
		c    io.Closer
	}{
		{"host", srv.api.host},
		{"renter", srv.api.renter},
		{"explorer", srv.api.explorer},
		{"miner", srv.api.miner},
		{"wallet", srv.api.wallet},
		{"tpool", srv.api.tpool},
		{"consensus", srv.api.cs},
		{"gateway", srv.api.gateway},
	}
	for _, mod := range mods {
		if mod.c != nil {
			if err := mod.c.Close(); err != nil {
				errs = append(errs, fmt.Errorf("%v.Close failed: %v", mod.name, err))
			}
		}
	}

	return build.JoinErrors(errs, "\n")
}
