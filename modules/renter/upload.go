package renter

// upload.go performs basic preprocessing on upload requests and then adds the
// requested files into the repair heap.
//
// TODO: Currently you cannot upload a directory using the api, if you want to
// upload a directory you must make 1 api call per file in that directory.
// Perhaps we should extend this endpoint to be able to recursively add files in
// a directory?
//
// TODO: Currently the minimum contracts check is not enforced while testing,
// which means that code is not covered at all. Enabling enforcement during
// testing will probably break a ton of existing tests, which means they will
// all need to be fixed when we do enable it, but we should enable it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HyperspaceApp/Hyperspace/build"
	"github.com/HyperspaceApp/Hyperspace/crypto"
	"github.com/HyperspaceApp/Hyperspace/modules"
	"github.com/HyperspaceApp/Hyperspace/modules/renter/siafile"
	"github.com/HyperspaceApp/errors"
)

var (
	// errUploadDirectory is returned if the user tries to upload a directory.
	errUploadDirectory = errors.New("cannot upload directory")
)

// validateSource verifies that a sourcePath meets the
// requirements for upload.
func validateSource(sourcePath string) error {
	// Check for read access
	file, err := os.Open(sourcePath)
	if err != nil {
		return errors.AddContext(err, "unable to open the source file")
	}
	file.Close()

	finfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if finfo.IsDir() {
		return errUploadDirectory
	}

	return nil
}

// Upload instructs the renter to start tracking a file. The renter will
// automatically upload and repair tracked files using a background loop.
func (r *Renter) Upload(up modules.FileUploadParams) error {
	// Enforce nickname rules.
	if err := validateSiapath(up.HyperspacePath); err != nil {
		return err
	}
	// Enforce source rules.
	if err := validateSource(up.Source); err != nil {
		return err
	}

	// Delete existing file if overwrite flag is set. Ignore ErrUnknownPath.
	if up.Force {
		if err := r.DeleteFile(up.HyperspacePath); err != nil && err != siafile.ErrUnknownPath {
			return err
		}
	}

	// Check for a nickname conflict.
	lockID := r.mu.RLock()
	exists, _ := r.staticFileSet.Exists(up.HyperspacePath)
	r.mu.RUnlock(lockID)
	if exists {
		// Remove existing file if overwrite is specified
		if up.Force {
			err := r.DeleteFile(up.HyperspacePath)
			// Return of ErrUnknownPath should not prevent upload
			if err != nil && err != siafile.ErrUnknownPath {
				return err
			}
		} else {
			return siafile.ErrPathOverload
		}
	}

	// Fill in any missing upload params with sensible defaults.
	fileInfo, err := os.Stat(up.Source)
	if err != nil {
		return err
	}
	if up.ErasureCode == nil {
		up.ErasureCode, _ = siafile.NewRSCode(defaultDataPieces, defaultParityPieces)
	}

	// Check that we have contracts to upload to. We need at least data +
	// parity/2 contracts. NumPieces is equal to data+parity, and min pieces is
	// equal to parity. Therefore (NumPieces+MinPieces)/2 = (data+data+parity)/2
	// = data+parity/2.
	numContracts := len(r.hostContractor.Contracts())
	requiredContracts := (up.ErasureCode.NumPieces() + up.ErasureCode.MinPieces()) / 2
	if numContracts < requiredContracts && build.Release != "testing" {
		return fmt.Errorf("not enough contracts to upload file: got %v, needed %v", numContracts, (up.ErasureCode.NumPieces()+up.ErasureCode.MinPieces())/2)
	}

	// Create the directory path on disk. Renter directory is already present so
	// only files not in top level directory need to have directories created
	dir, _ := filepath.Split(up.HyperspacePath)
	dirHyperspacePath := strings.TrimSuffix(dir, "/")
	if dirHyperspacePath != "" {
		if err := r.createDir(dirHyperspacePath); err != nil {
			return err
		}
	}

	// Create the Siafile and add to renter
	entry, err := r.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.TypeDefaultRenter), uint64(fileInfo.Size()), fileInfo.Mode())
	if err != nil {
		return err
	}
	defer entry.Close()

	// Send the upload to the repair loop.
	hosts := r.managedRefreshHostsAndWorkers()
	id := r.mu.Lock()
	unfinishedChunks := r.buildUnfinishedChunks(entry.ChunkEntrys(), hosts)
	r.mu.Unlock(id)
	for i := 0; i < len(unfinishedChunks); i++ {
		r.uploadHeap.managedPush(unfinishedChunks[i])
	}
	select {
	case r.uploadHeap.newUploads <- struct{}{}:
	default:
	}
	return nil
}
