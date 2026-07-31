package model

import "time"

type ContactValue struct {
	Value   string `json:"value" yaml:"value"`
	Label   string `json:"label,omitempty" yaml:"label,omitempty"`
	Source  string `json:"source,omitempty" yaml:"source,omitempty"`
	Primary bool   `json:"primary,omitempty" yaml:"primary,omitempty"`
}

// Card holds the single-valued parts of a contact card. The field names follow
// the vCard vocabulary that Apple Contacts, Google Contacts and CardDAV all
// share, so a value keeps its meaning instead of being flattened into a
// display name. Birthday is text because address books record year-less
// birthdays as --MM-DD.
type Card struct {
	GivenName                string `json:"given_name,omitempty" yaml:"given_name,omitempty"`
	MiddleName               string `json:"middle_name,omitempty" yaml:"middle_name,omitempty"`
	FamilyName               string `json:"family_name,omitempty" yaml:"family_name,omitempty"`
	PreviousFamilyName       string `json:"previous_family_name,omitempty" yaml:"previous_family_name,omitempty"`
	NamePrefix               string `json:"name_prefix,omitempty" yaml:"name_prefix,omitempty"`
	NameSuffix               string `json:"name_suffix,omitempty" yaml:"name_suffix,omitempty"`
	Nickname                 string `json:"nickname,omitempty" yaml:"nickname,omitempty"`
	PhoneticGivenName        string `json:"phonetic_given_name,omitempty" yaml:"phonetic_given_name,omitempty"`
	PhoneticMiddleName       string `json:"phonetic_middle_name,omitempty" yaml:"phonetic_middle_name,omitempty"`
	PhoneticFamilyName       string `json:"phonetic_family_name,omitempty" yaml:"phonetic_family_name,omitempty"`
	PhoneticOrganizationName string `json:"phonetic_organization_name,omitempty" yaml:"phonetic_organization_name,omitempty"`
	OrganizationName         string `json:"organization_name,omitempty" yaml:"organization_name,omitempty"`
	DepartmentName           string `json:"department_name,omitempty" yaml:"department_name,omitempty"`
	JobTitle                 string `json:"job_title,omitempty" yaml:"job_title,omitempty"`
	Birthday                 string `json:"birthday,omitempty" yaml:"birthday,omitempty"`
	Note                     string `json:"note,omitempty" yaml:"note,omitempty"`
}

type ExternalRef struct {
	ID         string    `json:"id,omitempty" yaml:"id,omitempty"`
	Resource   string    `json:"resource,omitempty" yaml:"resource,omitempty"`
	ETag       string    `json:"etag,omitempty" yaml:"etag,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at,omitzero" yaml:"last_seen_at,omitempty"`
}

type AvatarRef struct {
	Source    string    `json:"source,omitempty" yaml:"source,omitempty"`
	MIME      string    `json:"mime,omitempty" yaml:"mime,omitempty"`
	SHA256    string    `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	Width     int       `json:"width,omitempty" yaml:"width,omitempty"`
	Height    int       `json:"height,omitempty" yaml:"height,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero" yaml:"updated_at,omitempty"`
	Data      []byte    `json:"-" yaml:"-"`
}

type PersonSource struct {
	Names      []string            `json:"names,omitempty" yaml:"names,omitempty"`
	Tags       []string            `json:"tags,omitempty" yaml:"tags,omitempty"`
	Emails     []string            `json:"emails,omitempty" yaml:"emails,omitempty"`
	Phones     []string            `json:"phones,omitempty" yaml:"phones,omitempty"`
	Addresses  []string            `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Accounts   map[string][]string `json:"accounts,omitempty" yaml:"accounts,omitempty"`
	LastSeenAt time.Time           `json:"last_seen_at,omitzero" yaml:"last_seen_at,omitempty"`
}

type Person struct {
	ID                 string `json:"id" yaml:"id"`
	Name               string `json:"name" yaml:"name"`
	SortName           string `json:"sort_name,omitempty" yaml:"sort_name,omitempty"`
	Card               `yaml:",inline"`
	AKA                []string                  `json:"aka,omitempty" yaml:"aka,omitempty"`
	Tags               []string                  `json:"tags,omitempty" yaml:"tags,omitempty"`
	Emails             []ContactValue            `json:"emails,omitempty" yaml:"emails,omitempty"`
	Phones             []ContactValue            `json:"phones,omitempty" yaml:"phones,omitempty"`
	Addresses          []ContactValue            `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	URLAddresses       []ContactValue            `json:"url_addresses,omitempty" yaml:"url_addresses,omitempty"`
	SocialProfiles     []ContactValue            `json:"social_profiles,omitempty" yaml:"social_profiles,omitempty"`
	InstantMessages    []ContactValue            `json:"instant_message_addresses,omitempty" yaml:"instant_message_addresses,omitempty"`
	Dates              []ContactValue            `json:"dates,omitempty" yaml:"dates,omitempty"`
	ContactRelations   []ContactValue            `json:"contact_relations,omitempty" yaml:"contact_relations,omitempty"`
	Avatar             AvatarRef                 `json:"avatar,omitzero" yaml:"avatar,omitempty"`
	Accounts           map[string][]string       `json:"accounts,omitempty" yaml:"accounts,omitempty"`
	Sources            map[string]PersonSource   `json:"sources,omitempty" yaml:"sources,omitempty"`
	Apple              ExternalRef               `json:"apple,omitzero" yaml:"apple,omitempty"`
	Google             ExternalRef               `json:"google,omitzero" yaml:"google,omitempty"`
	Annotation         string                    `json:"annotation,omitempty" yaml:"annotation,omitempty"`
	AnnotationStatedAt string                    `json:"annotation_stated_at,omitempty" yaml:"annotation_stated_at,omitempty"`
	CreatedAt          time.Time                 `json:"created_at" yaml:"created_at"`
	UpdatedAt          time.Time                 `json:"updated_at" yaml:"updated_at"`
	Path               string                    `json:"path,omitempty" yaml:"-"`
	Body               string                    `json:"body,omitempty" yaml:"-"`
	Extra              map[string]map[string]any `json:"extra,omitempty" yaml:"-"`
}

type Note struct {
	ID         string    `json:"id" yaml:"id"`
	PersonID   string    `json:"person_id" yaml:"person_id"`
	OccurredAt time.Time `json:"occurred_at" yaml:"occurred_at"`
	CapturedAt time.Time `json:"captured_at" yaml:"captured_at"`
	Kind       string    `json:"kind" yaml:"kind"`
	Source     string    `json:"source" yaml:"source"`
	Account    string    `json:"account,omitempty" yaml:"account,omitempty"`
	ExternalID string    `json:"external_id,omitempty" yaml:"external_id,omitempty"`
	Direction  string    `json:"direction,omitempty" yaml:"direction,omitempty"`
	Confidence string    `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Topics     []string  `json:"topics,omitempty" yaml:"topics,omitempty"`
	FollowUpAt time.Time `json:"follow_up_at,omitzero" yaml:"follow_up_at,omitempty"`
	Privacy    string    `json:"privacy,omitempty" yaml:"privacy,omitempty"`
	Path       string    `json:"path,omitempty" yaml:"-"`
	Body       string    `json:"body,omitempty" yaml:"-"`
}

type SearchHit struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	PersonID  string    `json:"person_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Path      string    `json:"path"`
	Score     int       `json:"score"`
	Snippet   string    `json:"snippet,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
}
