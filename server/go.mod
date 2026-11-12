module NanoKVM-Server

go 1.26.10

require (
	github.com/creack/pty v1.1.24
	github.com/gin-gonic/gin v1.12.0
	github.com/go-playground/validator/v10 v10.30.6
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/jsonschema-go v0.4.3
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/mervick/aes-bridge-go v0.0.0-20250728011030-7e095cdac2f2
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/pion/dtls/v3 v3.1.15
	github.com/pion/ice/v4 v4.4.9
	github.com/pion/interceptor v0.1.51
	github.com/pion/rtcp v1.2.20
	github.com/pion/rtp v1.10.6
	github.com/pion/srtp/v3 v3.1.3
	github.com/pion/transport/v5 v5.1.5
	github.com/pion/webrtc/v4 v4.2.26
	github.com/rs/cors/wrapper/gin v0.0.0-20261101222852-7ca5fe28f7d5
	github.com/sirupsen/logrus v1.10.2
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.62.0
	golang.org/x/sys v0.50.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.5 // indirect
	github.com/bytedance/sonic/loader v0.5.3 // indirect
	github.com/cloudwego/base64x v0.1.7 // indirect
	github.com/gabriel-vasile/mimetype v1.4.16 // indirect
	github.com/gin-contrib/sse v1.2.1 // indirect
	github.com/go-playground/locales v0.14.2 // indirect
	github.com/go-playground/universal-translator v0.18.2 // indirect
	github.com/goccy/go-json v0.12.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.4.2 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pion/datachannel v1.6.4 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.2.2 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.12.1 // indirect
	github.com/pion/sdp/v3 v3.0.20 // indirect
	github.com/pion/stun/v4 v4.0.1 // indirect
	github.com/pion/turn/v5 v5.1.3 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.65.0 // indirect
	github.com/rs/cors v1.11.1 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.2 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.mongodb.org/mongo-driver/v2 v2.10.0 // indirect
	golang.org/x/arch v0.33.0 // indirect
	golang.org/x/oauth2 v0.38.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

// Packet-level AES-CTR hook; upstream base is v3.1.3.
replace github.com/pion/srtp/v3 => ./third_party/pion-srtp

replace github.com/pion/ice/v4 => ./third_party/pion-ice

// Server-side cipher negotiation follows NanoKVM's configured preference order.
replace github.com/pion/dtls/v3 => ./third_party/pion-dtls

// Fewer copies and allocations on the data channel's send path; upstream base is v1.12.1.
replace github.com/pion/sctp => ./third_party/pion-sctp
