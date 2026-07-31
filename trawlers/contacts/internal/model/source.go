package model

type SourceContact struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id,omitempty"`
	Name       string `json:"name"`
	Card
	Tags            []string            `json:"tags,omitempty"`
	Emails          []ContactValue      `json:"emails,omitempty"`
	Phones          []ContactValue      `json:"phones,omitempty"`
	Addresses       []ContactValue      `json:"addresses,omitempty"`
	URLAddresses    []ContactValue      `json:"url_addresses,omitempty"`
	SocialProfiles  []ContactValue      `json:"social_profiles,omitempty"`
	InstantMessages []ContactValue      `json:"instant_message_addresses,omitempty"`
	Dates           []ContactValue      `json:"dates,omitempty"`
	Relations       []ContactValue      `json:"contact_relations,omitempty"`
	Avatar          *SourceAvatar       `json:"avatar,omitempty"`
	Accounts        map[string][]string `json:"accounts,omitempty"`
	ETag            string              `json:"etag,omitempty"`
}

type SourceAvatar struct {
	Data   []byte `json:"-"`
	MIME   string `json:"mime,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	URL    string `json:"url,omitempty"`
}
