// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

package cubebox

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Spike switches for the pause/resume measurement campaign. The file is
// re-read once a second so an A/B arm can be flipped without a restart:
// one "name=1" per line, "#" comments and blanks ignored.
var (
	spikeMu     sync.Mutex
	spikeReadAt time.Time
	spikeValues map[string]string
)

func spikeOn(name string) bool {
	spikeMu.Lock()
	defer spikeMu.Unlock()
	if time.Since(spikeReadAt) > time.Second {
		spikeValues = map[string]string{}
		if data, err := os.ReadFile("/run/cubespike.cfg"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if key, value, ok := strings.Cut(line, "="); ok {
					spikeValues[strings.TrimSpace(key)] = strings.TrimSpace(value)
				}
			}
		}
		spikeReadAt = time.Now()
	}
	return spikeValues[name] == "1"
}
