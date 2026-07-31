package errcodes

// Problem is the stable safe error document returned by the Memento API.
type Problem struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	StatusCode  int               `json:"status_code"`
	FieldErrors map[string]string `json:"field_errors,omitempty"`
	RequestID   string            `json:"request_id,omitempty"`
}

// ProblemResponse is the stable Memento API error envelope.
type ProblemResponse struct {
	Error Problem `json:"error"`
}
