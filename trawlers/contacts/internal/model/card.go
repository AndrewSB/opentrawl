package model

import "strings"

// Card holds the names a contact card states about one person, beyond the
// single display name. The field names follow the vCard vocabulary that Apple
// Contacts, Google Contacts and CardDAV all share, so a value keeps its meaning
// instead of being flattened into a display name.
//
// Only name-bearing fields live here. A card states plenty more — birthdays,
// notes, related people — but those do not name the person, and this type
// exists so that a person can be found by what they are called.
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
}

// fields addresses every field in declaration order so that trimming, filling
// and comparison cannot silently forget one when a field is added. The receiver
// is a pointer because the returned pointers must address the caller's Card.
func (c *Card) fields() []*string {
	return []*string{
		&c.GivenName,
		&c.MiddleName,
		&c.FamilyName,
		&c.PreviousFamilyName,
		&c.NamePrefix,
		&c.NameSuffix,
		&c.Nickname,
		&c.PhoneticGivenName,
		&c.PhoneticMiddleName,
		&c.PhoneticFamilyName,
		&c.PhoneticOrganizationName,
		&c.OrganizationName,
		&c.DepartmentName,
		&c.JobTitle,
	}
}

// Clean trims every field. It never invents a value: a field the source left
// blank stays blank rather than being derived from the display name.
func (c Card) Clean() Card {
	cleaned := c
	for _, field := range cleaned.fields() {
		*field = strings.TrimSpace(*field)
	}
	return cleaned
}

func (c Card) IsZero() bool {
	return c.Clean() == Card{}
}

// Fill takes each field the receiver is missing from other. Sources merge in a
// stable order, so the first source to state a field owns it and a later sparse
// card cannot blank it.
func (c Card) Fill(other Card) Card {
	filled, from := c.Clean(), other.Clean()
	into, source := filled.fields(), from.fields()
	for i := range into {
		if *into[i] == "" {
			*into[i] = *source[i]
		}
	}
	return filled
}

// SearchNames are the card fields that name the person under a different
// spelling from their display name. A contact filed under a circumstance —
// where and when someone was met — is reachable only through these.
//
// Given, middle and family names are absent on purpose: they already compose
// the display name, so adding them would match nothing new. Job title and
// department are absent because they describe a role, not a person.
func (c Card) SearchNames() []string {
	c = c.Clean()
	return []string{
		c.Nickname,
		c.PreviousFamilyName,
		c.PhoneticGivenName,
		c.PhoneticMiddleName,
		c.PhoneticFamilyName,
		c.PhoneticOrganizationName,
		c.OrganizationName,
	}
}
