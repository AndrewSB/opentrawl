package archive

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

// personFiledUnderACircumstance is the case this whole path exists for: a
// contact whose display name records where and when they were met rather than
// what they are called. Their card still names them.
func personFiledUnderACircumstance(t *testing.T) (context.Context, *Store) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "contacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	person := model.NewPerson("Boat trip Croatia 2019", now)
	person.Card = model.Card{
		GivenName:          "Avery",
		FamilyName:         "Stone",
		Nickname:           "Ave",
		PreviousFamilyName: "Rivers",
		PhoneticFamilyName: "STOHN",
		OrganizationName:   "Harbour Books",
	}
	if _, err := store.UpsertPerson(ctx, person); err != nil {
		t.Fatal(err)
	}
	return ctx, store
}

func TestPersonIsFoundByTheNamesOnTheirCard(t *testing.T) {
	ctx, store := personFiledUnderACircumstance(t)
	for _, query := range []string{"Ave", "Rivers", "STOHN", "Harbour Books"} {
		candidates, err := store.ResolvePeople(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 1 {
			t.Errorf("%q found %d people; a card name did not resolve", query, len(candidates))
			continue
		}
		if candidates[0].PersonDisplayName != "Boat trip Croatia 2019" {
			t.Errorf("%q resolved to %q", query, candidates[0].PersonDisplayName)
		}
	}
}

// Finding a person by a name and then not being able to say which name it was
// leaves the result unexplained, which is what the matched-value field is for.
func TestResolvedPersonReportsTheCardNameThatMatched(t *testing.T) {
	ctx, store := personFiledUnderACircumstance(t)
	candidates, err := store.ResolvePeople(ctx, "Rivers")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one person, got %d", len(candidates))
	}
	if got := candidates[0].PersonNameOrHumanReadableContactValueThatMatchedQuery; got != "Rivers" {
		t.Fatalf("the match was reported as %q, not the card name that resolved it", got)
	}
}

// A card name says what someone is called. It is not evidence that a chat
// participant is that person, so it must not widen cross-service matching.
func TestCardNamesStayOutOfIdentityAliases(t *testing.T) {
	ctx, store := personFiledUnderACircumstance(t)
	people, err := store.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 {
		t.Fatalf("expected one person, got %d", len(people))
	}
	for _, alias := range resolverIdentityAliases(people[0]) {
		if alias == "Harbour Books" || alias == "Ave" {
			t.Fatalf("card name %q became an identity alias", alias)
		}
	}
}

func TestCardSurvivesAStorageRoundTrip(t *testing.T) {
	ctx, store := personFiledUnderACircumstance(t)
	people, err := store.People(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 {
		t.Fatalf("expected one person, got %d", len(people))
	}
	card := people[0].Card
	if card.Nickname != "Ave" || card.PreviousFamilyName != "Rivers" || card.OrganizationName != "Harbour Books" {
		t.Fatalf("the card did not survive storage: %+v", card)
	}
}
