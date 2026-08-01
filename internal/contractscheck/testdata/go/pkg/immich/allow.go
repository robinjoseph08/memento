package immich

import (
	"bytes"
	"encoding/json"
)

type providerRequest struct {
	IDs []string `json:"ids"`
}

type providerResponse struct {
	ID *string `json:"id"`
}

type providerResponses []providerResponse

type localSerializedEnvelope struct {
	Extra json.RawMessage `json:"extra"`
}

func AllowedDependencyContracts(client *Client) error {
	if _, err := marshalJSONRequest(providerRequest{IDs: []string{"one"}}); err != nil {
		return err
	}
	var response providerResponse
	if err := client.doJSON(0, "POST", "assets", nil, providerRequest{IDs: []string{"one"}}, &response, nil); err != nil {
		return err
	}
	if err := client.getJSON(0, "assets/one", &response, nil); err != nil {
		return err
	}
	var responses providerResponses
	if err := client.getJSONQuery(0, "assets", nil, &responses, nil); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	return json.Unmarshal([]byte(`{"id":"one"}`), &fields)
}

func AllowedLocalSerialization() error {
	local := localSerializedEnvelope{Extra: json.RawMessage(`{"id":"one"}`)}
	if _, err := json.Marshal(local); err != nil {
		return err
	}
	var body bytes.Buffer
	return json.NewEncoder(&body).Encode(local)
}
