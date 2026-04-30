package api

import "io/fs"

// frontendFS is replaced in production by the go:embed FS from the frontend package.
var frontendFS fs.FS = emptyFS{}

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
