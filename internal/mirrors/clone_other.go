//go:build !linux

package mirrors

import "os"

func cloneFile(dst, src *os.File) bool { return false }
