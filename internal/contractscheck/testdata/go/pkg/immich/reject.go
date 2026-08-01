package immich

import (
	"bytes"
	"encoding/json"
	"time"
)

type rawProviderResponse struct {
	Extra json.RawMessage `json:"extra"`
}

type requestMap map[string]string

type responseMapList []map[string]string

type responseInterfaceList []any

type responseScalarList []string

type responseAnonymousList []struct {
	ID string `json:"id"`
}

type responseScalar string

type ExportedProviderResponse struct {
	ID *string `json:"id"`
}

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

func RawMessageResponse(client *Client) error {
	var response rawProviderResponse
	return client.getJSON(0, "assets/one", &response, nil)
}

func NamedSliceOfMapResponse(client *Client) error {
	var response responseMapList
	return client.getJSON(0, "assets", &response, nil)
}

func NamedSliceOfInterfaceResponse(client *Client) error {
	var response responseInterfaceList
	return client.getJSON(0, "assets", &response, nil)
}

func NamedSliceOfScalarResponse(client *Client) error {
	var response responseScalarList
	return client.getJSON(0, "assets", &response, nil)
}

func NamedSliceOfAnonymousResponse(client *Client) error {
	var response responseAnonymousList
	return client.getJSON(0, "assets", &response, nil)
}

func NamedScalarResponse(client *Client) error {
	var response responseScalar
	return client.getJSON(0, "assets", &response, nil)
}

func ExternalStructResponse(client *Client) error {
	var response time.Time
	return client.getJSON(0, "assets", &response, nil)
}

func ExportedStructResponse(client *Client) error {
	var response ExportedProviderResponse
	return client.getJSON(0, "assets", &response, nil)
}

func RawBytesResponse(client *Client) error {
	var response []byte
	return client.getJSON(0, "assets", &response, nil)
}

func UnresolvedResponse[Response any](client *Client, response Response) error {
	return client.getJSON(0, "assets", &response, nil)
}

func callGet(get func(int, string, any, error) error) error {
	var response providerResponse
	return get(0, "assets/one", &response, nil)
}

func HigherOrderMethodValue(client *Client) error {
	return callGet(client.getJSON)
}

func ReturnedMethodValue(client *Client) func(int, string, any, error) error {
	return client.getJSON
}

func ReturnedMethodExpression() func(*Client, int, string, any, error) error {
	return (*Client).getJSON
}

func ReturnedGetJSONQuery(client *Client) func(int, string, any, any, error) error {
	return client.getJSONQuery
}

func ReturnedDoJSONStatus(client *Client) func(int, string, string, any, any, any, error, int) error {
	return client.doJSONStatus
}

func callMarshal(marshal func(providerRequest) ([]byte, error)) error {
	_, err := marshal(providerRequest{})
	return err
}

func HigherOrderMarshal() error {
	return callMarshal(marshalJSONRequest[providerRequest])
}

func ReturnedMarshal() func(providerRequest) ([]byte, error) {
	return marshalJSONRequest[providerRequest]
}

func ReturnedResponseWrapper() func(*Client, any) error {
	return forwardResponse
}

type ExportedProviderRequest struct {
	ID string `json:"id"`
}

type requestWithAnonymous struct {
	Nested struct {
		ID string `json:"id"`
	} `json:"nested"`
}

type requestWithMap struct {
	Nested map[string]string `json:"nested"`
}

type requestWithInterface struct {
	Nested any `json:"nested"`
}

type requestWithExternalObject struct {
	Nested bytes.Buffer `json:"nested"`
}

type requestWithValueLeaves struct {
	When  time.Time     `json:"when"`
	State responseState `json:"state"`
}

type responseState string

type genericEnvelope[T any] struct {
	Value T `json:"value"`
}

type responseWithRawFields struct {
	Untagged json.RawMessage
	Generic  genericEnvelope[json.RawMessage] `json:"generic"`
	Ignored  json.RawMessage                  `json:"-"`
	local    json.RawMessage
}

type localClient interface {
	getJSON(int, string, any, error) error
	getJSONQuery(int, string, any, any, error) error
	doJSON(int, string, string, any, any, any, error) error
	doJSONStatus(int, string, string, any, any, any, error, int) error
}

func ExportedRequestRoot() error {
	_, err := marshalJSONRequest(ExportedProviderRequest{ID: "one"})
	return err
}

func AnonymousRequestField() error {
	_, err := marshalJSONRequest(requestWithAnonymous{})
	return err
}

func MapRequestField() error {
	_, err := marshalJSONRequest(requestWithMap{})
	return err
}

func InterfaceRequestField() error {
	_, err := marshalJSONRequest(requestWithInterface{})
	return err
}

func ExternalObjectRequestField() error {
	_, err := marshalJSONRequest(requestWithExternalObject{})
	return err
}

func AllowedRequestValueLeaves() error {
	_, err := marshalJSONRequest(requestWithValueLeaves{})
	return err
}

func SemanticRawMessageFields(client *Client) error {
	var response responseWithRawFields
	return client.getJSON(0, "assets/one", &response, nil)
}

func LocalInterfaceBindResponse(client localClient) error {
	response := map[string]string{}
	return client.getJSON(0, "assets/one", &response, nil)
}

func LocalInterfaceQueryResponse(client localClient) error {
	response := map[string]string{}
	return client.getJSONQuery(0, "assets", nil, &response, nil)
}

func LocalInterfaceDoRequest(client localClient) error {
	var response providerResponse
	return client.doJSON(0, "POST", "assets", nil, map[string]string{"id": "one"}, &response, nil)
}

func LocalInterfaceDoStatusResponse(client localClient) error {
	response := map[string]string{}
	return client.doJSONStatus(0, "POST", "assets", nil, providerRequest{}, &response, nil, 200)
}
