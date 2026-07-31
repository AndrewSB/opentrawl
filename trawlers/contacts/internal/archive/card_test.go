package archive

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestContactSnapshotStoresAndReplacesTheWholeCard(t *testing.T) {
	ctx := context.Background()
	st := openTempStore(t)
	now := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	contact := model.SourceContact{
		ExternalID: "apple-1",
		Name:       "Ada Example",
		Card: model.Card{
			GivenName: "Ada", FamilyName: "Example", Nickname: "Ace", PreviousFamilyName: "Byron",
			OrganizationName: "Analytical Engines", JobTitle: "Mathematician", Birthday: "--12-10",
			Note: "Prefers written updates.",
		},
		Emails:          []model.ContactValue{{Value: "ada@example.com", Label: "work"}},
		URLAddresses:    []model.ContactValue{{Value: "https://example.com/ada", Label: "homepage"}},
		SocialProfiles:  []model.ContactValue{{Value: "ada", Label: "mastodon"}, {Value: "ada", Label: "matrix"}},
		InstantMessages: []model.ContactValue{{Value: "+15550100", Label: "signal"}},
		Dates:           []model.ContactValue{{Value: "2015-07-08", Label: "anniversary"}},
		Relations:       []model.ContactValue{{Value: "Grace Example", Label: "spouse"}},
	}
	if _, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{contact}, now); err != nil {
		t.Fatal(err)
	}

	person, err := st.FindPerson(ctx, "Ada Example")
	if err != nil {
		t.Fatal(err)
	}
	if person.Nickname != "Ace" || person.JobTitle != "Mathematician" || person.Birthday != "--12-10" {
		t.Fatalf("card = %#v", person.Card)
	}
	// One handle on two services is two facts, not a duplicate.
	if len(person.SocialProfiles) != 2 {
		t.Fatalf("social profiles = %#v", person.SocialProfiles)
	}
	for _, values := range [][]model.ContactValue{person.URLAddresses, person.InstantMessages, person.Dates, person.ContactRelations} {
		if len(values) != 1 {
			t.Fatalf("card values = %#v", values)
		}
	}

	// A nickname is an alias: searching it finds the Person.
	results, _, err := st.Search(ctx, "Ace", SearchOptions{Limit: 5})
	if err != nil || len(results) == 0 {
		t.Fatalf("nickname search results = %#v err = %v", results, err)
	}

	// The source owns the card, so a field it stops stating stops being reported.
	contact.Nickname = ""
	contact.SocialProfiles = nil
	if _, err := st.SyncContactSnapshot(ctx, "apple", []model.SourceContact{contact}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	person, err = st.FindPerson(ctx, "Ada Example")
	if err != nil {
		t.Fatal(err)
	}
	if person.Nickname != "" || len(person.SocialProfiles) != 0 {
		t.Fatalf("withdrawn card values = %#v %#v", person.Card, person.SocialProfiles)
	}
	if person.JobTitle != "Mathematician" {
		t.Fatalf("retained card = %#v", person.Card)
	}
}

// TestArchiveMigratesPreCardSchema opens an archive written before contact
// cards existed. Its rows must survive, and it must accept the card value
// kinds its own CREATE TABLE never allowed.
func TestArchiveMigratesPreCardSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "contacts.db")
	legacy, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`create table people (
		  id text primary key, name text not null, sort_name text not null default '',
		  aka_json text not null default '[]', tags_json text not null default '[]',
		  avatar_json text not null default '{}', accounts_json text not null default '{}',
		  sources_json text not null default '{}', apple_json text not null default '{}',
		  google_json text not null default '{}', body text not null default '',
		  annotation text not null default '', annotation_stated_at text not null default '',
		  created_at text not null, updated_at text not null
		)`,
		`create table contact_values (
		  person_id text not null references people(id) on delete cascade,
		  kind text not null check(kind in ('email', 'phone', 'address')),
		  position integer not null, value text not null default '', label text not null default '',
		  source text not null default '', primary_value integer not null default 0,
		  primary key(person_id, kind, position)
		)`,
		`create index idx_contact_values_person on contact_values(person_id, kind, position)`,
		`insert into people(id, name, annotation, created_at, updated_at)
		 values ('person_legacy', 'Ada Example', 'Kept across the migration.', '2026-07-09T10:00:00Z', '2026-07-09T10:00:00Z')`,
		`insert into contact_values(person_id, kind, position, value, label, source, primary_value)
		 values ('person_legacy', 'email', 0, 'ada@example.com', 'work', 'apple', 1)`,
	} {
		if _, err := legacy.DB().ExecContext(ctx, statement); err != nil {
			_ = legacy.Close()
			t.Fatal(err)
		}
	}
	if err := legacy.EnsureSchemaVersion(ctx, 2); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	person, err := st.Person(ctx, "person_legacy")
	if err != nil {
		t.Fatal(err)
	}
	if person.Annotation != "Kept across the migration." || len(person.Emails) != 1 || person.Emails[0].Value != "ada@example.com" {
		t.Fatalf("migrated person = %#v", person)
	}
	person.Card = model.Card{Nickname: "Ace"}
	person.SocialProfiles = []model.ContactValue{{Value: "ada", Label: "mastodon", Source: "apple"}}
	if err := st.SavePerson(ctx, person); err != nil {
		t.Fatal(err)
	}
	reloaded, err := st.Person(ctx, "person_legacy")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Nickname != "Ace" || len(reloaded.SocialProfiles) != 1 {
		t.Fatalf("card after migration = %#v %#v", reloaded.Card, reloaded.SocialProfiles)
	}
}
