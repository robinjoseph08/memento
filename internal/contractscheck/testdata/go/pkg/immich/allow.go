package immich

import (
	"encoding/json"
)

type providerRequest struct {
	IDs []string `json:"ids"`
}

type providerResponse struct {
	ID *string `json:"id"`
}

func AllowedDependencyContracts(client *Client) error {
	if _, err := json.Marshal(providerRequest{IDs: []string{"one"}}); err != nil {
		return err
	}
	var response providerResponse
	if err := client.getJSON(0, "assets/one", &response, nil); err != nil {
		return err
	}
	var responses []providerResponse
	if err := client.getJSONQuery(0, "assets", nil, &responses, nil); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	return json.Unmarshal([]byte(`{"id":"one"}`), &fields)
}
