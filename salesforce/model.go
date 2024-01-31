package salesforce

// QueryResponse see https://ellogroup.atlassian.net/wiki/spaces/EP/pages/13402137/Salesforce+Package#QueryResponse%5BE-any%5D
// for more detail on below
// NB. if more models added here please update the above page
type QueryResponse[E any] struct {
	TotalSize int  `json:"totalSize"`
	Done      bool `json:"done"`
	Records   []E  `json:"records"`
}

// PostResponse is the response from Salesforce for a post/create request
type PostResponse struct {
	Id      string `json:"id"`
	Success bool   `json:"success"`
}

// Attributes to be added, optionally, to concrete types of E for QueryResponse[E]
type Attributes struct {
	Type string `json:"type"`
	Url  string `json:"url"`
}
