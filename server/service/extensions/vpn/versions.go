package vpn

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

var versionPattern = regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[-+][a-zA-Z0-9.-]+)*`)
var mu sync.Mutex
var cached map[string]string
var expires time.Time

func Versions(c *gin.Context) {
	mu.Lock()
	defer mu.Unlock()
	if time.Now().After(expires) {
		cached = make(map[string]string)
		for name, tool := range map[string]string{"tailscale": "tailscale", "wireguard": "wg", "openvpn": "nkos-openvpn3"} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			b, err := exec.CommandContext(ctx, tool, "--version").Output()
			cancel()
			if err == nil {
				cached[name] = versionPattern.FindString(strings.SplitN(string(b), "\n", 2)[0])
			}
		}
		expires = time.Now().Add(time.Minute)
	}
	var rsp proto.Response
	rsp.OkRspWithData(c, cached)
}
