package syncer

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Darwin has no /proc; ps reports the owner and full arguments.
func findStrays(workRoot string) ([]int, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,uid=,command=").Output()
	if err != nil {
		return nil, err
	}
	self, uid := os.Getpid(), strconv.Itoa(os.Getuid())
	found := map[int]bool{}
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != uid {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self {
			continue
		}
		if referencesWork(workRoot, fields[2:]) {
			found[pid] = true
		}
	}
	return sortedPIDs(found), nil
}
