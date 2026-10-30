package syncer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Darwin has no /proc; ps reports the owner and full arguments.
func findStrays(ctx context.Context, workRoot string) ([]int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,uid=,command=")
	cmd.WaitDelay = time.Second
	var output processListBuffer
	cmd.Stdout = &output
	err := cmd.Run()
	out := output.Bytes()
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

// processListBuffer bounds the OS process inventory before parsing it.
type processListBuffer struct{ bytes.Buffer }

func (b *processListBuffer) Write(p []byte) (int, error) {
	const maximum = 8 << 20
	if len(p) > maximum-b.Len() {
		return 0, fmt.Errorf("process list exceeds %d bytes", maximum)
	}
	return b.Buffer.Write(p)
}
