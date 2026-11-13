package remote

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
)

const APIProtocol = 1
const SkillProtocol = 1

type ServerMetadata struct {
	Mode           string   `json:"mode"`
	Protocol       int      `json:"protocol"`
	SkillProtocol  int      `json:"skill_protocol,omitempty"`
	OutputProtocol int      `json:"cli_output_protocol,omitempty"`
	Version        string   `json:"version,omitempty"`
	Build          string   `json:"build,omitempty"`
	Capabilities   []string `json:"capabilities"`
}

// Explicit session declarations survive a machine CLI upgrade. Unknown protocols
// fail before transport, so a new tool cannot reinterpret an old session silently.
func CheckClientProtocols(binding Binding) error {
	if (binding.APIProtocol != 0 && binding.APIProtocol != APIProtocol) || (binding.SkillProtocol != 0 && binding.SkillProtocol != SkillProtocol) {
		return errors.New("REMOTE_BINDING_PROTOCOL_UNSUPPORTED: this client supports API 1 and Skill 1; preserve the project requirements")
	}
	for _, name := range []string{"AEL_API_PROTOCOL", "AEL_SKILL_PROTOCOL"} {
		if value := os.Getenv(name); value != "" && value != "1" {
			return errors.New("CLIENT_SESSION_PROTOCOL_UNSUPPORTED: " + name + " requires another tool protocol; this client supports 1")
		}
	}
	return nil
}

func Metadata(client *http.Client, profile Profile) (ServerMetadata, error) {
	var meta ServerMetadata
	response, err := request(client, profile, "/api/v1/meta", false)
	if err != nil {
		return meta, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return meta, errors.New("AUTH_REQUIRED: server metadata access was rejected")
	}
	if response.StatusCode == http.StatusForbidden {
		return meta, errors.New("FORBIDDEN: server metadata access was rejected")
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&meta) != nil || meta.Mode != "server" || meta.Protocol != APIProtocol {
		return meta, errors.New("REMOTE_PROTOCOL_UNSUPPORTED: server API must support protocol 1")
	}
	if meta.SkillProtocol != 0 && meta.SkillProtocol != SkillProtocol {
		return meta, errors.New("REMOTE_SKILL_PROTOCOL_UNSUPPORTED: server Skill protocol differs from this client")
	}
	if meta.SkillProtocol == 0 {
		meta.SkillProtocol = SkillProtocol
	}
	return meta, nil
}

func checkCapability(client *http.Client, profile Profile, capability string) error {
	meta, err := Metadata(client, profile)
	if err != nil {
		return err
	}
	if !slices.Contains(meta.Capabilities, capability) {
		return errors.New("REMOTE_PROTOCOL_UNSUPPORTED: server lacks the required " + capability + " capability")
	}
	return nil
}
