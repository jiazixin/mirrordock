//go:build !linux && !darwin

package mirrors

import (
	"errors"
	"os"
)

func cloneFile(src *os.File, target string) error { return errors.ErrUnsupported }
func fileIdentity(info os.FileInfo) string        { return "" }
func fileOwnership(info os.FileInfo) string       { return "" }
func hardlinkIdentity(info os.FileInfo) string    { return "" }
