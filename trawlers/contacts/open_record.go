package contacts

import (
	"context"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	contactsopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/contacts/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

type openValue struct {
	ref    string
	person model.Person
	notes  []model.Note
}

var _ trawlkit.RecordOpener = (*App)(nil)

func (a *App) OpenRecord(ctx context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	value, err := a.loadOpenPerson(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	machine := projectOpenRecord(value)
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{SourceId: a.Info().ID, OpenRef: machine.GetRef(), Data: data, Presentation: projectOpenPresentation(value)}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func projectOpenRecord(value openValue) *contactsopenv1.ContactsRecord {
	ref, person := value.ref, value.person
	record := &contactsopenv1.ContactsRecord{
		Ref:                     ref,
		Name:                    person.Name,
		Aka:                     append([]string(nil), person.AKA...),
		Tags:                    append([]string(nil), person.Tags...),
		Emails:                  projectContactValues(person.Emails),
		Phones:                  projectContactValues(person.Phones),
		Addresses:               projectContactValues(person.Addresses),
		UrlAddresses:            projectContactValues(person.URLAddresses),
		SocialProfiles:          projectContactValues(person.SocialProfiles),
		InstantMessageAddresses: projectContactValues(person.InstantMessages),
		Dates:                   projectContactValues(person.Dates),
		ContactRelations:        projectContactValues(person.ContactRelations),
		Accounts:                make(map[string]*contactsopenv1.IdentifierList, len(person.Accounts)),
	}
	if record.Ref == "" {
		record.Ref = archive.PersonRef(person.ID)
	}
	setOptionalString(&record.SortName, person.SortName)
	projectOpenCard(record, person.Card)
	for name, identifiers := range person.Accounts {
		record.Accounts[name] = &contactsopenv1.IdentifierList{Values: append([]string(nil), identifiers...)}
	}
	setOptionalString(&record.Annotation, person.Annotation)
	setOptionalString(&record.AnnotationStatedAt, person.AnnotationStatedAt)
	return record
}

func projectOpenCard(record *contactsopenv1.ContactsRecord, card model.Card) {
	setOptionalString(&record.GivenName, card.GivenName)
	setOptionalString(&record.MiddleName, card.MiddleName)
	setOptionalString(&record.FamilyName, card.FamilyName)
	setOptionalString(&record.PreviousFamilyName, card.PreviousFamilyName)
	setOptionalString(&record.NamePrefix, card.NamePrefix)
	setOptionalString(&record.NameSuffix, card.NameSuffix)
	setOptionalString(&record.Nickname, card.Nickname)
	setOptionalString(&record.PhoneticGivenName, card.PhoneticGivenName)
	setOptionalString(&record.PhoneticMiddleName, card.PhoneticMiddleName)
	setOptionalString(&record.PhoneticFamilyName, card.PhoneticFamilyName)
	setOptionalString(&record.PhoneticOrganizationName, card.PhoneticOrganizationName)
	setOptionalString(&record.OrganizationName, card.OrganizationName)
	setOptionalString(&record.DepartmentName, card.DepartmentName)
	setOptionalString(&record.JobTitle, card.JobTitle)
	setOptionalString(&record.Birthday, card.Birthday)
	setOptionalString(&record.Note, card.Note)
}

func projectContactValues(values []model.ContactValue) []*contactsopenv1.ContactValue {
	records := make([]*contactsopenv1.ContactValue, 0, len(values))
	for _, value := range values {
		record := &contactsopenv1.ContactValue{Value: value.Value}
		setOptionalString(&record.Label, value.Label)
		if value.Primary {
			record.Primary = recordBool(true)
		}
		records = append(records, record)
	}
	return records
}

func setOptionalString(target **string, value string) {
	if value != "" {
		*target = &value
	}
}

func recordBool(value bool) *bool { return &value }

func projectOpenPresentation(value openValue) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Name)
	if title == "" {
		title = "Contact"
	}
	card := value.person.Card
	fields := make([]*presentationv1.Field, 0, 7)
	appendPresentationFieldWithAnchor(&fields, "Sort name", value.person.SortName, "sort_name")
	appendPresentationFieldWithAnchor(&fields, "Identifier", value.person.ID, "identifier")
	appendPresentationFieldWithAnchor(&fields, "Nickname", card.Nickname, "nickname")
	appendPresentationFieldWithAnchor(&fields, "Also known as", joinPresentationStrings(record.Aka), "aka")
	appendPresentationFieldWithAnchor(&fields, "Maiden name", card.PreviousFamilyName, "previous_family_name")
	appendPresentationFieldWithAnchor(&fields, "Phonetic name", joinPresentationName(card.PhoneticGivenName, card.PhoneticMiddleName, card.PhoneticFamilyName), "phonetic_name")
	appendPresentationFieldWithAnchor(&fields, "Job title", card.JobTitle, "job_title")
	appendPresentationFieldWithAnchor(&fields, "Department", card.DepartmentName, "department_name")
	appendPresentationFieldWithAnchor(&fields, "Organization", card.OrganizationName, "organization_name")
	appendPresentationFieldWithAnchor(&fields, "Birthday", card.Birthday, "birthday")
	appendPresentationFieldWithAnchor(&fields, "Tags", joinPresentationStrings(record.Tags), "tag")
	appendPresentationFieldWithAnchor(&fields, "Emails", formatPresentationContactValues(record.Emails), "email")
	appendPresentationFieldWithAnchor(&fields, "Phones", formatPresentationContactValues(record.Phones), "phone")
	appendPresentationFieldWithAnchor(&fields, "Addresses", formatPresentationContactValues(record.Addresses), "address")
	appendPresentationFieldWithAnchor(&fields, "Websites", formatPresentationContactValues(record.UrlAddresses), "url_address")
	appendPresentationFieldWithAnchor(&fields, "Social profiles", formatPresentationContactValues(record.SocialProfiles), "social_profile")
	appendPresentationFieldWithAnchor(&fields, "Instant messages", formatPresentationContactValues(record.InstantMessageAddresses), "instant_message_address")
	appendPresentationFieldWithAnchor(&fields, "Dates", formatPresentationContactValues(record.Dates), "date")
	appendPresentationFieldWithAnchor(&fields, "Related names", formatPresentationContactValues(record.ContactRelations), "contact_relation")
	appendPresentationFieldWithAnchor(&fields, "Accounts", formatPresentationAccounts(record.Accounts), "account")
	appendPresentationFieldWithAnchor(&fields, "Source names", formatPresentationSourceNames(value.person.Sources), "source_name")
	appendPresentationFieldWithAnchor(&fields, "Note", card.Note, "note")
	appendPresentationFieldWithAnchor(&fields, "Annotation", record.GetAnnotation(), "annotation")
	blocks := make([]*presentationv1.Block, 0, 3+len(value.notes))
	blocks = append(blocks, &presentationv1.Block{AnchorId: "name", Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: title}}})
	if len(fields) > 0 {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}})
	}
	if body := strings.TrimSpace(value.person.Body); body != "" {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "body", Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: body}}})
	}
	for _, note := range value.notes {
		noteFields := make([]*presentationv1.Field, 0, 4)
		appendPresentationField(&noteFields, "Kind", note.Kind)
		appendPresentationField(&noteFields, "Source", note.Source)
		appendPresentationField(&noteFields, "Topics", joinPresentationStrings(note.Topics))
		appendPresentationField(&noteFields, "Body", note.Body)
		if len(noteFields) > 0 {
			blocks = append(blocks, &presentationv1.Block{
				AnchorId: archive.NoteAnchorID(note.ID),
				Content:  &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: noteFields}},
			})
		}
	}
	return &presentationv1.PresentationDocument{Title: title, Blocks: blocks, PrimaryAnchorId: "name"}
}

func formatPresentationSourceNames(values map[string]model.PersonSource) string {
	names := []string{}
	for _, source := range values {
		names = append(names, source.Names...)
	}
	sort.Strings(names)
	return joinPresentationStrings(names)
}

func appendPresentationField(fields *[]*presentationv1.Field, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value})
	}
}

func appendPresentationFieldWithAnchor(fields *[]*presentationv1.Field, label, value, anchorID string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value, AnchorId: anchorID})
	}
}

// joinPresentationName reads the parts of one name as a name, not as a list.
func joinPresentationName(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func joinPresentationStrings(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return strings.Join(items, ", ")
}

func formatPresentationContactValues(values []*contactsopenv1.ContactValue) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil || strings.TrimSpace(value.Value) == "" {
			continue
		}
		item := strings.TrimSpace(value.Value)
		if label := strings.TrimSpace(value.GetLabel()); label != "" {
			item += " (" + label + ")"
		}
		if value.GetPrimary() {
			item += " [primary]"
		}
		items = append(items, item)
	}
	return strings.Join(items, ", ")
}

func formatPresentationAccounts(values map[string]*contactsopenv1.IdentifierList) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		list := values[key]
		if list == nil {
			continue
		}
		identifiers := make([]string, 0, len(list.Values))
		for _, value := range list.Values {
			if value = strings.TrimSpace(value); value != "" {
				identifiers = append(identifiers, value)
			}
		}
		if len(identifiers) != 0 {
			items = append(items, strings.TrimSpace(key)+": "+strings.Join(identifiers, ", "))
		}
	}
	return strings.Join(items, "; ")
}
