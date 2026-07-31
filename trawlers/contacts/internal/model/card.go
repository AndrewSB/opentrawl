package model

import "strings"

// CardKinds are the labelled card values stored alongside emails, phones and
// postal addresses. Each one is a repeated, labelled field on the source card.
const (
	KindURLAddress     = "url_address"
	KindSocialProfile  = "social_profile"
	KindInstantMessage = "instant_message_address"
	KindDate           = "date"
	KindRelation       = "contact_relation"
)

// Clean trims every card field. It never invents a value: a card the source
// left blank stays blank rather than being derived from the display name.
func (c Card) Clean() Card {
	return Card{
		GivenName:                strings.TrimSpace(c.GivenName),
		MiddleName:               strings.TrimSpace(c.MiddleName),
		FamilyName:               strings.TrimSpace(c.FamilyName),
		PreviousFamilyName:       strings.TrimSpace(c.PreviousFamilyName),
		NamePrefix:               strings.TrimSpace(c.NamePrefix),
		NameSuffix:               strings.TrimSpace(c.NameSuffix),
		Nickname:                 strings.TrimSpace(c.Nickname),
		PhoneticGivenName:        strings.TrimSpace(c.PhoneticGivenName),
		PhoneticMiddleName:       strings.TrimSpace(c.PhoneticMiddleName),
		PhoneticFamilyName:       strings.TrimSpace(c.PhoneticFamilyName),
		PhoneticOrganizationName: strings.TrimSpace(c.PhoneticOrganizationName),
		OrganizationName:         strings.TrimSpace(c.OrganizationName),
		DepartmentName:           strings.TrimSpace(c.DepartmentName),
		JobTitle:                 strings.TrimSpace(c.JobTitle),
		Birthday:                 strings.TrimSpace(c.Birthday),
		Note:                     strings.TrimSpace(c.Note),
	}
}

func (c Card) IsZero() bool {
	return c.Clean() == Card{}
}

// Fill takes each field the receiver is missing from other. Sources are merged
// in a stable order, so the first source to state a field owns it and a later
// sparse card cannot blank it.
func (c Card) Fill(other Card) Card {
	c, other = c.Clean(), other.Clean()
	fields := []struct {
		into *string
		from string
	}{
		{&c.GivenName, other.GivenName},
		{&c.MiddleName, other.MiddleName},
		{&c.FamilyName, other.FamilyName},
		{&c.PreviousFamilyName, other.PreviousFamilyName},
		{&c.NamePrefix, other.NamePrefix},
		{&c.NameSuffix, other.NameSuffix},
		{&c.Nickname, other.Nickname},
		{&c.PhoneticGivenName, other.PhoneticGivenName},
		{&c.PhoneticMiddleName, other.PhoneticMiddleName},
		{&c.PhoneticFamilyName, other.PhoneticFamilyName},
		{&c.PhoneticOrganizationName, other.PhoneticOrganizationName},
		{&c.OrganizationName, other.OrganizationName},
		{&c.DepartmentName, other.DepartmentName},
		{&c.JobTitle, other.JobTitle},
		{&c.Birthday, other.Birthday},
		{&c.Note, other.Note},
	}
	for _, field := range fields {
		if *field.into == "" {
			*field.into = field.from
		}
	}
	return c
}

// SearchNames are the card fields that name the person under a different
// spelling. They belong in the alias index so that searching a nickname,
// maiden name or phonetic spelling finds the Person.
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
