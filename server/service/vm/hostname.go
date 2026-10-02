package vm

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	BootHostnameFile = "/boot/hostname"
	EtcHostname      = "/etc/hostname"
	EtcHosts         = "/etc/hosts"
)

var hostnameFiles = struct{ boot, etc, hosts string }{BootHostnameFile, EtcHostname, EtcHosts}

var hostnameLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// validHostname applies the rule the DHCP scripts (S30eth, S30wifi) use, so
// a name the UI accepts is never silently dropped from DHCP requests, and
// S10uuid never receives spaces or shell/sed metacharacters.
func validHostname(name string) error {
	if len(name) < 1 || len(name) > 253 {
		return errors.New("hostname must be 1-253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if !hostnameLabel.MatchString(label) {
			return errors.New("hostname labels may contain only letters, digits and inner hyphens (1-63 characters)")
		}
	}
	return nil
}

// replaceHostEntry renames oldName to newName where it appears as a whole
// host name in /etc/hosts, leaving addresses, comments and other names
// (for example "localhost" when the old name was "local") untouched.
func replaceHostEntry(hosts, oldName, newName string) string {
	if oldName == "" {
		return hosts
	}
	lines := strings.Split(hosts, "\n")
	for i, line := range lines {
		body, comment, hasComment := strings.Cut(line, "#")
		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		changed := false
		for j := 1; j < len(fields); j++ {
			if fields[j] == oldName {
				fields[j] = newName
				changed = true
			}
		}
		if !changed {
			continue
		}
		lines[i] = strings.Join(fields, "\t")
		if hasComment {
			lines[i] += "\t#" + comment
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Service) SetHostname(c *gin.Context) {
	var req proto.SetHostnameReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if err := validHostname(req.Hostname); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}

	dataRead, err := os.ReadFile(hostnameFiles.etc)
	if err != nil {
		rsp.ErrRsp(c, -1, "read Hostname failed")
		return
	}

	oldHostname := strings.TrimSpace(string(dataRead))

	if oldHostname != req.Hostname {
		dataRead, err = os.ReadFile(hostnameFiles.hosts)
		if err != nil {
			rsp.ErrRsp(c, -1, "read Hosts failed")
			return
		}

		data := []byte(replaceHostEntry(string(dataRead), oldHostname, req.Hostname))

		if err := atomicfile.Write(hostnameFiles.hosts, data, 0o644); err != nil {
			rsp.ErrRsp(c, -2, "failed to write data")
			return
		}
	}

	data := []byte(req.Hostname)

	if err := atomicfile.Write(hostnameFiles.boot, data, 0o644); err != nil {
		rsp.ErrRsp(c, -2, "failed to write data")
		return
	}

	if err := atomicfile.Write(hostnameFiles.etc, data, 0o644); err != nil {
		rsp.ErrRsp(c, -3, "failed to write data")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("set Hostname: %s", req.Hostname)

	if err := exec.Command("hostname", "-F", hostnameFiles.etc).Run(); err != nil {
		log.Warnf("apply hostname: %v", err)
	}
}

func (s *Service) GetHostname(c *gin.Context) {
	var rsp proto.Response

	data, err := os.ReadFile(hostnameFiles.etc)
	if err != nil {
		rsp.ErrRsp(c, -1, "read Hostname failed")
		return
	}

	rsp.OkRspWithData(c, &proto.GetHostnameRsp{
		Hostname: strings.Replace(string(data), "\n", "", -1),
	})
	log.Debugf("get Hostname successful")
}
