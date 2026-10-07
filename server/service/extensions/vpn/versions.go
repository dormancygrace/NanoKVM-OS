package vpn

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"NanoKVM-Server/internal/toolcache"
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

var versionPattern = regexp.MustCompile(`[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[-+][a-zA-Z0-9.-]+)*`)

var tools = map[string]string{"tailscale": "tailscale", "wireguard": "wg", "openvpn": "openvpn", "netbird": "netbird"}

// The VPN pages ask every 5 s; a version only changes with the program file.
var versions = toolcache.New(func(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, args...).Output()
	return string(b), err
}, time.Minute)

func Versions(c *gin.Context) {
	result := make(map[string]string)
	for name, tool := range tools {
		path, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if out, err := versions.Output(path, "--version"); err == nil {
			result[name] = versionPattern.FindString(strings.SplitN(out, "\n", 2)[0])
		}
	}
	var rsp proto.Response
	rsp.OkRspWithData(c, result)
}
