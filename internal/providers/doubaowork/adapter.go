package doubaowork

import "github.com/caigee-cmd/cli2api/internal/providers"

// Adapter wires the capability surface. Login opens a short-lived browser for
// the Cookie; chat itself stays in-process HTTP.
func (c *Client) Adapter() providers.Adapter {
	return providers.Adapter{
		ID:           "doubaowork",
		Credential:   credentialCodec{},
		Login:        c,
		Chat:         c,
		Models:       c,
		Classifier:   classifier{},
		ImportExport: importer{},
		Prober:       c,

		SessionInvalidator: c,
	}
}

type classifier struct{}

func (classifier) Classify(status int, body string) providers.ClassifiedError {
	return Classify(status, body)
}
