package immich

import "encoding/json"

type promotedProviderFields struct {
	NestedMap       map[string]string `json:"nested_map"`
	NestedInterface any               `json:"nested_interface"`
	NestedAnonymous struct {
		ID string `json:"id"`
	} `json:"nested_anonymous"`
	Raw     json.RawMessage   `json:"raw"`
	hidden  map[string]string `json:"hidden"`
	Ignored json.RawMessage   `json:"-"`
}

type responseWithPromotedFields struct {
	promotedProviderFields
	ignoredProviderFields `json:"-"`
	hidden                map[string]string `json:"hidden"`
	Ignored               json.RawMessage   `json:"-"`
}

type ignoredProviderFields struct {
	Nested any             `json:"nested"`
	Raw    json.RawMessage `json:"raw"`
}

func PromotedProviderFields(client *Client) error {
	var response responseWithPromotedFields
	return client.getJSON(0, "assets/one", &response, nil)
}
