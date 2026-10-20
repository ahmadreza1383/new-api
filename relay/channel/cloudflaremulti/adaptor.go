package cloudflaremulti

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/cloudflare"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// Adaptor adds account-specific authentication to the existing Cloudflare
// protocol implementation. Request conversion and response handling stay there.
type Adaptor struct {
	cloudflare.Adaptor
	credentialError error
}

var _ channel.Adaptor = (*Adaptor)(nil)

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	credential, err := ParseCredential(info.ApiKey)
	a.credentialError = err
	if err != nil {
		return
	}
	// Only request-local metadata is resolved. The original key in the Gin
	// context remains intact for multi-key failure tracking and recovery.
	info.ApiKey = credential.APIKey
	info.ApiVersion = credential.AccountID
	a.Adaptor.Init(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.credentialError != nil {
		return "", a.credentialError
	}
	return a.Adaptor.GetRequestURL(info)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, headers *http.Header, info *relaycommon.RelayInfo) error {
	if a.credentialError != nil {
		return a.credentialError
	}
	return a.Adaptor.SetupRequestHeader(c, headers, info)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (any, error) {
	// Pass the composed adaptor so the transport uses its credential guards.
	return channel.DoApiRequest(a, c, info, body)
}
