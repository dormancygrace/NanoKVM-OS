package network

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/stream/webrtcdtls"
)

// The STUN and TURN servers of the WebRTC video, audio and RustDesk
// connections, kept in server.yaml. Changes apply to new connections.
const maxStunServers = 4

var (
	webRTCUpdateMu sync.Mutex
	readWebRTC     = config.WebRTCSettings
	updateWebRTC   = func(w config.WebRTC) error {
		if err := config.UpdateWebRTC(w); err != nil {
			return err
		}
		applyDTLSPreference()
		return nil
	}
)

// applyDTLSPreference points the DTLS policy at the stored preference.
func applyDTLSPreference() {
	webrtcdtls.SetPreference(func() string { return config.WebRTCSettings().DTLSCipher })
}

// InitWebRTC applies the stored DTLS preference at start.
func InitWebRTC() { applyDTLSPreference() }

type webrtcServersTurn struct {
	Address       string `json:"address"`
	Username      string `json:"username"`
	HasCredential bool   `json:"hasCredential"`
}

type webrtcServersRsp struct {
	// StunMode is default (the built-in set), custom or off.
	StunMode    string            `json:"stunMode"`
	Stun        []string          `json:"stun"`
	DefaultStun []string          `json:"defaultStun"`
	Turn        webrtcServersTurn `json:"turn"`
	// DTLSCipher is chacha20 or aes-gcm: the suite offered first.
	DTLSCipher string `json:"dtlsCipher"`
}

type webrtcServersReq struct {
	StunMode   string   `json:"stunMode"`
	Stun       []string `json:"stun"`
	DTLSCipher string   `json:"dtlsCipher"`
	Turn       struct {
		Address  string `json:"address"`
		Username string `json:"username"`
		// Credential nil or empty keeps the stored one.
		Credential *string `json:"credential"`
	} `json:"turn"`
}

func splitStun(stun string) []string {
	return strings.FieldsFunc(stun, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

func webrtcServersState() webrtcServersRsp {
	settings := readWebRTC()
	stun, turn := settings.Stun, settings.Turn
	rsp := webrtcServersRsp{StunMode: "custom", Stun: splitStun(stun), DefaultStun: splitStun(config.DefaultStun),
		Turn:       webrtcServersTurn{Address: turn.TurnAddr, Username: turn.TurnUser, HasCredential: turn.TurnCred != ""},
		DTLSCipher: settings.DTLSCipher}
	switch stun {
	case config.DefaultStun:
		rsp.StunMode = "default"
	case "disable":
		rsp.StunMode, rsp.Stun = "off", []string{}
	}
	return rsp
}

// iceAddress returns host:port of a STUN or TURN server, without a scheme.
func iceAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	for _, scheme := range []string{"stun:", "turn:"} {
		value = strings.TrimPrefix(value, scheme)
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", errors.New("server must be host:port")
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return "", errors.New("port must be between 1 and 65535")
	}
	if net.ParseIP(host) == nil && !validHostname(host) {
		return "", errors.New("invalid server host")
	}
	return net.JoinHostPort(host, port), nil
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// printable reports whether s is 1 to max printable ASCII characters
// without spaces, as STUN and TURN credentials are sent.
func printable(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r > '~' {
			return false
		}
	}
	return true
}

// webrtcServersConfig validates a request against the stored TURN password.
func webrtcServersConfig(req webrtcServersReq, current config.Turn) (config.WebRTC, error) {
	if req.DTLSCipher == "" {
		req.DTLSCipher = config.DTLSChaCha20 // a page from before the choice
	}
	if req.DTLSCipher != config.DTLSChaCha20 && req.DTLSCipher != config.DTLSAESGCM {
		return config.WebRTC{}, errors.New("DTLS cipher must be chacha20 or aes-gcm")
	}
	stun, turn, err := webrtcServers(req, current)
	return config.WebRTC{Stun: stun, Turn: turn, DTLSCipher: req.DTLSCipher}, err
}

func webrtcServers(req webrtcServersReq, current config.Turn) (string, config.Turn, error) {
	var stun string
	switch req.StunMode {
	case "default":
	case "off":
		stun = "disable"
	case "custom":
		var servers []string
		seen := map[string]bool{}
		for _, value := range req.Stun {
			if strings.TrimSpace(value) == "" {
				continue
			}
			address, err := iceAddress(value)
			if err != nil {
				return "", config.Turn{}, err
			}
			if !seen[address] {
				seen[address] = true
				servers = append(servers, address)
			}
		}
		if len(servers) == 0 {
			return "", config.Turn{}, errors.New("add a STUN server or turn STUN off")
		}
		if len(servers) > maxStunServers {
			return "", config.Turn{}, errors.New("at most 4 STUN servers")
		}
		stun = strings.Join(servers, ",")
	default:
		return "", config.Turn{}, errors.New("STUN mode must be default, custom or off")
	}

	if strings.TrimSpace(req.Turn.Address) == "" {
		return stun, config.Turn{}, nil
	}
	address, err := iceAddress(req.Turn.Address)
	if err != nil {
		return "", config.Turn{}, err
	}
	if !printable(req.Turn.Username, 128) {
		return "", config.Turn{}, errors.New("TURN needs a username of up to 128 characters without spaces")
	}
	credential := current.TurnCred
	if req.Turn.Credential != nil && *req.Turn.Credential != "" {
		credential = *req.Turn.Credential
	}
	if !printable(credential, 256) {
		return "", config.Turn{}, errors.New("TURN needs a password of up to 256 characters without spaces")
	}
	return stun, config.Turn{TurnAddr: address, TurnUser: req.Turn.Username, TurnCred: credential}, nil
}

func (s *Service) GetWebRTCServers(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, webrtcServersState())
}

func (s *Service) SetWebRTCServers(c *gin.Context) {
	var req webrtcServersReq
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	// Retaining a password is a read-modify-write operation: do not restore
	// an old password over another administrator's concurrent update.
	webRTCUpdateMu.Lock()
	defer webRTCUpdateMu.Unlock()
	settings, err := webrtcServersConfig(req, readWebRTC().Turn)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if err = updateWebRTC(settings); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	log.Infof("WebRTC: STUN %s, TURN %t, DTLS %s first", req.StunMode, settings.Turn.TurnAddr != "", settings.DTLSCipher)
	rsp.OkRspWithData(c, webrtcServersState())
}
