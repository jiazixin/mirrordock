//go:build !linux && !darwin

package mirrors

import "errors"

func diskSpace(dir string) (DiskSpace, error) { return DiskSpace{}, errors.ErrUnsupported }
