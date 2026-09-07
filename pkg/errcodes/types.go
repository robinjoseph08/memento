package errcodes

// ErrorResponse is the browser API's shared error contract.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	StatusCode int               `json:"status_code"`
	Fields     map[string]string `json:"fields,omitempty"`
}
