package immich

import "encoding/json"

type Client struct{}

func marshalJSONRequest[Request any](request Request) ([]byte, error) {
	return json.Marshal(request)
}

func (c *Client) getJSON(ctx int, path string, target any, statusError error) error {
	return c.getJSONQuery(ctx, path, nil, target, statusError)
}

func (c *Client) getJSONQuery(ctx int, path string, query any, target any, statusError error) error {
	return c.doJSON(ctx, "GET", path, query, nil, target, statusError)
}

func (c *Client) doJSON(ctx int, method, path string, query any, request any, target any, statusError error) error {
	return c.doJSONStatus(ctx, method, path, query, request, target, statusError, 200)
}

func (*Client) doJSONStatus(_ int, _, _ string, _ any, request any, _ any, _ error, _ int) error {
	if request != nil {
		_, _ = marshalJSONRequest(request)
	}
	return nil
}
