package model

import (
	"strings"
	"testing"
)

func TestCardCleanTrimsWithoutInventingValues(t *testing.T) {
	card := Card{GivenName: "  Avery ", Nickname: "\tAve\n"}.Clean()
	if card.GivenName != "Avery" || card.Nickname != "Ave" {
		t.Fatalf("unexpected clean card: %+v", card)
	}
	// A field the source left blank stays blank. Deriving it from the display
	// name would invent a name the card never stated.
	if card.FamilyName != "" || card.OrganizationName != "" {
		t.Fatalf("clean invented a value: %+v", card)
	}
	if (Card{GivenName: "   "}).IsZero() != true {
		t.Fatal("a card of only blanks is not zero")
	}
}

// Sources merge in a stable order, so the first source to state a field owns
// it: a later sparse card must not blank a name the person is findable by.
func TestCardFillTakesOnlyMissingFields(t *testing.T) {
	existing := Card{Nickname: "Ave", OrganizationName: "Old Corp"}
	incoming := Card{Nickname: "Avery", FamilyName: "Stone", OrganizationName: ""}
	filled := existing.Fill(incoming)
	if filled.Nickname != "Ave" {
		t.Fatalf("fill overwrote a stated field: %+v", filled)
	}
	if filled.FamilyName != "Stone" {
		t.Fatalf("fill did not take a missing field: %+v", filled)
	}
	if filled.OrganizationName != "Old Corp" {
		t.Fatalf("a blank incoming field blanked a stated one: %+v", filled)
	}
}

// Every field must be reachable through fields(), or trimming and filling
// silently forget whichever one was added last.
func TestCardFillCoversEveryField(t *testing.T) {
	var full Card
	for i, field := range full.fields() {
		*field = "value"
		_ = i
	}
	filled := Card{}.Fill(full)
	for _, field := range filled.fields() {
		if *field != "value" {
			t.Fatal("a card field is not reachable through fields(), so Clean and Fill skip it")
		}
	}
}

func TestCardSearchNamesAreTheAlternativeSpellings(t *testing.T) {
	card := Card{
		GivenName:          "Avery",
		FamilyName:         "Stone",
		Nickname:           "Ave",
		PreviousFamilyName: "Rivers",
		PhoneticFamilyName: "STOHN",
		OrganizationName:   "Harbour Books",
		JobTitle:           "Bookseller",
	}
	names := strings.Join(card.SearchNames(), "|")
	for _, want := range []string{"Ave", "Rivers", "STOHN", "Harbour Books"} {
		if !strings.Contains(names, want) {
			t.Errorf("search names omit %q: %s", want, names)
		}
	}
	// Given and family names already compose the display name, so adding them
	// would match nothing new. A job title describes a role, not a person.
	for _, unwanted := range []string{"Avery", "Stone", "Bookseller"} {
		if strings.Contains(names, unwanted) {
			t.Errorf("search names include %q, which does not name the person differently: %s", unwanted, names)
		}
	}
}
