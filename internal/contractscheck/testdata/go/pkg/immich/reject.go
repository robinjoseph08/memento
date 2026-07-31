package immich

import "encoding/json"

type rawProviderResponse struct {
	Extra json.RawMessage `json:"extra"`
}

func AnonymousMarshal() error {
	_, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: "one"})
	return err
}

func MapMarshal() error {
	_, err := json.Marshal(map[string]string{"id": "one"})
	return err
}

func AnonymousGet(client *Client) error {
	response := struct {
		ID string `json:"id"`
	}{}
	return client.getJSON(0, "assets/one", &response, nil)
}

func MapGetQuery(client *Client) error {
	response := map[string]string{}
	return client.getJSONQuery(0, "assets", nil, &response, nil)
}

func AnonymousDo(client *Client) error {
	return client.doJSON(0, "POST", "assets", nil, nil, &struct{ ID string }{}, nil)
}

func MapDoStatus(client *Client) error {
	return client.doJSONStatus(0, "POST", "assets", nil, nil, &map[string]string{}, nil, 201)
}
