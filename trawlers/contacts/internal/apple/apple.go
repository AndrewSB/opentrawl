package apple

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func ActionableReadError(err error) error {
	switch sourceStateForError(err) {
	case SourceNeedsFullDiskAccess:
		return fmt.Errorf("cannot read Apple Contacts; grant OpenTrawl Full Disk Access in System Settings > Privacy & Security > Full Disk Access: %w", err)
	case SourceInvalid:
		return fmt.Errorf("could not read Apple Contacts data: %w", err)
	default:
		return fmt.Errorf("could not access Apple Contacts: %w", err)
	}
}

type SourceState string

const (
	SourceReady               SourceState = "ready"
	SourceNeedsFullDiskAccess SourceState = "needs_full_disk_access"
	SourceUnavailable         SourceState = "unavailable"
	SourceInvalid             SourceState = "invalid"
)

type invalidSourceError struct {
	Path string
	Err  error
}

func (e invalidSourceError) Error() string {
	return "invalid Apple AddressBook source " + e.Path + ": " + e.Err.Error()
}

func (e invalidSourceError) Unwrap() error {
	return e.Err
}

func isSourcePermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "permission denied") ||
		strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "not authorized") ||
		strings.Contains(message, "authorization denied")
}

func sourceStateForError(err error) SourceState {
	if err == nil {
		return SourceReady
	}
	var invalidErr invalidSourceError
	if errors.As(err, &invalidErr) {
		return SourceInvalid
	}
	if isSourcePermissionError(err) {
		return SourceNeedsFullDiskAccess
	}
	return SourceUnavailable
}

// Contact is one Apple Contacts card as stored, using the vCard field names
// Apple itself exposes. Nothing here is derived: a card that states a nickname,
// a job title or a maiden name keeps it as that field rather than having it
// folded into a display name.
type Contact struct {
	Identifier      string         `json:"identifier"`
	model.Card                     // given_name, nickname, job_title, birthday, note, ...
	FullName        string         `json:"full_name"`
	Emails          []LabeledValue `json:"emails"`
	Phones          []LabeledValue `json:"phones"`
	Addresses       []LabeledValue `json:"addresses,omitempty"`
	URLAddresses    []LabeledValue `json:"url_addresses,omitempty"`
	SocialProfiles  []LabeledValue `json:"social_profiles,omitempty"`
	InstantMessages []LabeledValue `json:"instant_message_addresses,omitempty"`
	Dates           []LabeledValue `json:"dates,omitempty"`
	Relations       []LabeledValue `json:"contact_relations,omitempty"`
	AvatarData      []byte         `json:"avatar_data,omitempty"`
}

// LabeledValue is any repeated card field. Apple attaches a label to every one
// of them, so the label travels with the value instead of being discarded.
type LabeledValue struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

func (v *LabeledValue) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		*v = LabeledValue{Value: value}
		return nil
	}
	type labeledValue LabeledValue
	var parsed labeledValue
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*v = LabeledValue(parsed)
	return nil
}

func (c Contact) Name() string {
	if strings.TrimSpace(c.FullName) != "" {
		return strings.TrimSpace(c.FullName)
	}
	return strings.TrimSpace(strings.Join(nonEmptyStrings(c.GivenName, c.MiddleName, c.FamilyName), " "))
}

func (c Contact) SourceContact(includeAvatar bool) model.SourceContact {
	out := model.SourceContact{
		Source:          "apple",
		ExternalID:      c.Identifier,
		Name:            c.Name(),
		Card:            c.Clean(),
		Emails:          contactValues(c.Emails),
		Phones:          contactValues(c.Phones),
		Addresses:       contactValues(c.Addresses),
		URLAddresses:    contactValues(c.URLAddresses),
		SocialProfiles:  contactValues(c.SocialProfiles),
		InstantMessages: contactValues(c.InstantMessages),
		Dates:           contactValues(c.Dates),
		Relations:       contactValues(c.Relations),
	}
	if includeAvatar && len(c.AvatarData) > 0 {
		out.Avatar = &model.SourceAvatar{Data: append([]byte(nil), c.AvatarData...)}
	}
	return out
}

func contactValues(values []LabeledValue) []model.ContactValue {
	out := make([]model.ContactValue, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value.Value)
		if trimmed == "" {
			continue
		}
		out = append(out, model.ContactValue{
			Value:   trimmed,
			Label:   contactLabel(value.Label),
			Source:  "apple",
			Primary: len(out) == 0,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// contactLabel unwraps Apple's `_$!<Home>!$_` label encoding. A custom label a
// person typed themselves is kept as they typed it; only the wrapper and case
// of Apple's own labels are normalised.
func contactLabel(label string) string {
	label = strings.TrimSpace(label)
	if unwrapped := strings.TrimSuffix(strings.TrimPrefix(label, "_$!<"), ">!$_"); unwrapped != label {
		return strings.ToLower(strings.TrimSpace(unwrapped))
	}
	if label == "" {
		return "other"
	}
	return strings.ToLower(label)
}

func ReadFile(path string) ([]Contact, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Decode(f)
}

func Decode(r io.Reader) ([]Contact, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var contacts []Contact
		if err := json.Unmarshal([]byte(trimmed), &contacts); err != nil {
			return nil, err
		}
		return contacts, nil
	}
	var contacts []Contact
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Contact
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, scanner.Err()
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func ToSourceContacts(contacts []Contact, includeAvatars bool) []model.SourceContact {
	out := make([]model.SourceContact, 0, len(contacts))
	for _, contact := range contacts {
		if strings.TrimSpace(contact.Name()) == "" {
			continue
		}
		out = append(out, contact.SourceContact(includeAvatars))
	}
	return out
}
