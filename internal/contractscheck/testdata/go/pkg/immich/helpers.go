package immich

type Client struct{}

func (c *Client) getJSON(ctx int, path string, target any, statusError error) error {
	return c.getJSONQuery(ctx, path, nil, target, statusError)
}

func (c *Client) getJSONQuery(ctx int, path string, query any, target any, statusError error) error {
	return c.doJSON(ctx, "GET", path, query, nil, target, statusError)
}

func (c *Client) doJSON(ctx int, method, path string, query any, body []byte, target any, statusError error) error {
	return c.doJSONStatus(ctx, method, path, query, body, target, statusError, 200)
}

func (*Client) doJSONStatus(int, string, string, any, []byte, any, error, int) error {
	return nil
}
