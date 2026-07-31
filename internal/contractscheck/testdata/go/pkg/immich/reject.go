package immich

import (
	"bytes"
	"encoding/json"
)

type rawProviderResponse struct {
	Extra json.RawMessage `json:"extra"`
}

type requestMap map[string]string

func AnonymousRequest() error {
	_, err := marshalJSONRequest(struct {
		ID string `json:"id"`
	}{ID: "one"})
	return err
}

func MapRequest() error {
	_, err := marshalJSONRequest(map[string]string{"id": "one"})
	return err
}

func MapAliasRequest() error {
	_, err := marshalJSONRequest(requestMap{"id": "one"})
	return err
}

func RawRequest() error {
	_, err := marshalJSONRequest([]byte(`{"id":"one"}`))
	return err
}

func ErasedRequest() error {
	var request any = map[string]string{"id": "one"}
	_, err := marshalJSONRequest(request)
	return err
}

func AliasedRequestSeam() error {
	marshal := marshalJSONRequest[[]byte]
	_, err := marshal([]byte(`{"id":"one"}`))
	return err
}

func forwardRequest[Request any](request Request) error {
	_, err := marshalJSONRequest(request)
	return err
}

func WrappedRequest() error {
	return forwardRequest(map[string]string{"id": "one"})
}

func EncoderBuiltRequest() error {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(map[string]string{"id": "one"}); err != nil {
		return err
	}
	_, err := marshalJSONRequest(body)
	return err
}

func RawDo(client *Client) error {
	var response providerResponse
	return client.doJSON(0, "POST", "assets", nil, []byte(`{}`), &response, nil)
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

func ErasedResponse(client *Client) error {
	var response any = &providerResponse{}
	return client.getJSON(0, "assets/one", response, nil)
}

func UnknownResponse(client *Client) error {
	return client.getJSON(0, "assets/one", nil, nil)
}

func MethodValueResponse(client *Client) error {
	get := client.getJSON
	response := map[string]string{}
	return get(0, "assets/one", &response, nil)
}

func forwardResponse(client *Client, target any) error {
	return client.getJSON(0, "assets/one", target, nil)
}

func WrappedResponse(client *Client) error {
	response := map[string]string{}
	return forwardResponse(client, &response)
}

func MethodExpressionResponse(client *Client) error {
	response := map[string]string{}
	return (*Client).getJSON(client, 0, "assets/one", &response, nil)
}

func MethodValueRequest(client *Client) error {
	send := client.doJSON
	var response providerResponse
	return send(0, "POST", "assets", nil, []byte(`{}`), &response, nil)
}
