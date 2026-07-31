//go:build darwin

package apple

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func TestCheckSourceAtUsesOnlyAddressBookSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, addressBookDBName)
	createAddressBookFixture(t, path, []fixtureContact{{
		PK:         1,
		Identifier: "synthetic-contact:ABPerson",
		FirstName:  "Synthetic",
		LastName:   "Example",
	}})

	state, err := checkSourceAt(t.Context(), dir)
	t.Logf("raw source result: state=%q err=%q", state, err)
	if err != nil || state != SourceReady {
		t.Fatalf("state = %q, err = %v", state, err)
	}
}

func TestCheckSourceAtReportsUnavailableAndInvalidSourceStates(t *testing.T) {
	tests := []struct {
		name      string
		wantState SourceState
		make      func(t *testing.T, dir string)
	}{
		{
			name:      "missing directory",
			wantState: SourceUnavailable,
			make: func(t *testing.T, dir string) {
				t.Helper()
				_ = dir
			},
		},
		{
			name:      "invalid database",
			wantState: SourceInvalid,
			make: func(t *testing.T, dir string) {
				t.Helper()
				path := filepath.Join(dir, addressBookDBName)
				if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "AddressBook")
			if tt.name != "missing directory" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			tt.make(t, dir)
			state, err := checkSourceAt(t.Context(), dir)
			t.Logf("raw source result: state=%q err=%q", state, err)
			if state != tt.wantState || err == nil {
				t.Fatalf("state = %q, err = %v", state, err)
			}
		})
	}
}

func TestCheckAddressBookDatabasePreservesDisappearingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), addressBookDBName)
	err := checkAddressBookDatabase(t.Context(), path)
	state := sourceStateForError(err)
	t.Logf("raw source input: path=%q", path)
	t.Logf("raw source result: state=%q err=%q", state, err)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want not-exist", err)
	}
	if state != SourceUnavailable {
		t.Fatalf("state = %q, want %q", state, SourceUnavailable)
	}
}

func TestCheckSourceAtClassifiesPermissionErrors(t *testing.T) {
	if got := sourceStateForError(errors.New("operation not permitted")); got != SourceNeedsFullDiskAccess {
		t.Fatalf("state = %q, want %q", got, SourceNeedsFullDiskAccess)
	}
}

func TestCheckSourceAtReportsUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-zero fixture")
	}
	dir := filepath.Join(t.TempDir(), "AddressBook")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	state, err := checkSourceAt(t.Context(), dir)
	t.Logf("raw source result: state=%q err=%q", state, err)
	if state != SourceNeedsFullDiskAccess || err == nil {
		t.Fatalf("state = %q, err = %v", state, err)
	}
}

func TestReadAddressBookDirReadsRootAndSourceDatabases(t *testing.T) {
	dir := t.TempDir()
	createAddressBookFixture(t, filepath.Join(dir, addressBookDBName), []fixtureContact{{
		PK:         1,
		Identifier: "root-contact:ABPerson",
		FirstName:  "Root",
		LastName:   "Contact",
		Emails:     []string{"root@example.com"},
	}})
	birthday := time.Date(1815, 12, 10, 12, 0, 0, 0, time.UTC)
	sourceDir := filepath.Join(dir, "Sources", "source-1")
	createAddressBookFixture(t, filepath.Join(sourceDir, addressBookDBName), []fixtureContact{{
		PK:            1,
		Identifier:    "source-contact:ABPerson",
		FirstName:     "Ada",
		MiddleName:    "Augusta",
		LastName:      "Lovelace",
		MaidenName:    "Byron",
		Prefix:        "Dr",
		Suffix:        "PhD",
		Nickname:      "Countess",
		PhoneticFirst: "AY-duh",
		PhoneticLast:  "LUV-lace",
		Organisation:  "Analytical Engines",
		Department:    "Research",
		JobTitle:      "Mathematician",
		Birthday:      &birthday,
		Note:          "Met at the synthetic conference.",
		Phones:        []string{"+1 555 0100"},
		Emails:        []string{"ada@example.com"},
		Address: fixtureAddress{
			Label:       "_$!<Work>!$_",
			Street:      "1 Infinite Loop",
			City:        "Cupertino",
			State:       "CA",
			ZipCode:     "95014",
			CountryName: "United States",
			CountryCode: "US",
		},
		URLs:      []string{"https://example.com/ada"},
		Social:    []fixtureService{{Service: "Mastodon", Value: "ada"}},
		Messaging: []fixtureService{{Service: "Signal", Value: "+15550100"}},
		Dates:     []fixtureDate{{Label: "_$!<Anniversary>!$_", When: time.Date(2015, 7, 8, 12, 0, 0, 0, time.UTC)}},
		Related:   []fixtureService{{Service: "_$!<Spouse>!$_", Value: "Grace Example"}},
		Avatar:    []byte("avatar"),
	}})

	contacts, err := readAddressBookDir(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 2 {
		t.Fatalf("contacts = %#v", contacts)
	}
	if contacts[0].Identifier != "root-contact:ABPerson" || contacts[1].Identifier != "source-contact:ABPerson" {
		t.Fatalf("contact order = %#v", contacts)
	}
	source := contacts[1]
	if source.Name() != "Ada Augusta Lovelace" {
		t.Fatalf("name = %q", source.Name())
	}
	if len(source.Phones) != 1 || source.Phones[0].Value != "+1 555 0100" || source.Phones[0].Label != "_$!<Mobile>!$_" {
		t.Fatalf("phones = %#v", source.Phones)
	}
	if len(source.Emails) != 1 || source.Emails[0].Value != "ada@example.com" {
		t.Fatalf("emails = %#v", source.Emails)
	}
	if len(source.Addresses) != 1 {
		t.Fatalf("addresses = %#v", source.Addresses)
	}
	if source.Addresses[0].Label != "_$!<Work>!$_" {
		t.Fatalf("raw label = %#v", source.Addresses[0])
	}
	if source.Addresses[0].Value != "1 Infinite Loop\nCupertino CA 95014\nUnited States" {
		t.Fatalf("address value = %q", source.Addresses[0].Value)
	}
	if string(source.AvatarData) != "avatar" {
		t.Fatalf("avatar = %q", source.AvatarData)
	}

	wantCard := model.Card{
		GivenName: "Ada", MiddleName: "Augusta", FamilyName: "Lovelace", PreviousFamilyName: "Byron",
		NamePrefix: "Dr", NameSuffix: "PhD", Nickname: "Countess",
		PhoneticGivenName: "AY-duh", PhoneticFamilyName: "LUV-lace",
		OrganizationName: "Analytical Engines", DepartmentName: "Research", JobTitle: "Mathematician",
		Birthday: "1815-12-10", Note: "Met at the synthetic conference.",
	}
	if source.Card != wantCard {
		t.Fatalf("card = %#v, want %#v", source.Card, wantCard)
	}

	src := source.SourceContact(true)
	if len(src.Addresses) != 1 || src.Addresses[0].Label != "work" || src.Addresses[0].Source != "apple" {
		t.Fatalf("source address = %#v", src.Addresses)
	}
	if len(src.Phones) != 1 || src.Phones[0].Label != "mobile" {
		t.Fatalf("source phone label = %#v", src.Phones)
	}
	if len(src.Emails) != 1 || src.Emails[0].Label != "home" {
		t.Fatalf("source email label = %#v", src.Emails)
	}
	if src.Card != wantCard {
		t.Fatalf("source card = %#v, want %#v", src.Card, wantCard)
	}
	for _, check := range []struct {
		name   string
		values []model.ContactValue
		value  string
		label  string
	}{
		{"url", src.URLAddresses, "https://example.com/ada", "homepage"},
		{"social", src.SocialProfiles, "ada", "mastodon"},
		{"messaging", src.InstantMessages, "+15550100", "signal"},
		{"date", src.Dates, "2015-07-08", "anniversary"},
		{"relation", src.Relations, "Grace Example", "spouse"},
	} {
		if len(check.values) != 1 || check.values[0].Value != check.value || check.values[0].Label != check.label {
			t.Fatalf("%s values = %#v", check.name, check.values)
		}
	}
	if src.Avatar == nil || string(src.Avatar.Data) != "avatar" {
		t.Fatalf("source avatar = %#v", src.Avatar)
	}
}

func TestReadAddressBookKeepsNameOnlyCardsAndYearlessBirthdays(t *testing.T) {
	dir := t.TempDir()
	yearless := time.Date(yearlessDateYear, 3, 2, 12, 0, 0, 0, time.UTC)
	createAddressBookFixture(t, filepath.Join(dir, addressBookDBName), []fixtureContact{
		{PK: 1, Identifier: "name-only:ABPerson", FirstName: "Grace", LastName: "Example", Birthday: &yearless},
		{PK: 2, Identifier: "org-only:ABPerson", Organisation: "Synthetic Industries"},
	})

	contacts, err := readAddressBookDir(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 2 {
		t.Fatalf("contacts = %#v", contacts)
	}
	if contacts[0].Birthday != "--03-02" {
		t.Fatalf("yearless birthday = %q", contacts[0].Birthday)
	}
	if contacts[1].Name() != "Synthetic Industries" || contacts[1].OrganizationName != "Synthetic Industries" {
		t.Fatalf("organisation card = %#v", contacts[1])
	}
}

func TestReadAddressBookWithoutOptionalCardTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), addressBookDBName)
	createAddressBookFixture(t, path, []fixtureContact{{
		PK: 1, Identifier: "sparse:ABPerson", FirstName: "Ada", LastName: "Example", Nickname: "Ace",
	}})
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ZABCDNOTE", "ZABCDURLADDRESS", "ZABCDSOCIALPROFILE", "ZABCDMESSAGINGADDRESS", "ZABCDDATE", "ZABCDRELATEDNAME"} {
		if _, err := db.Exec("drop table " + table); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	contacts, err := readAddressBookDatabase(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Nickname != "Ace" {
		t.Fatalf("contacts = %#v", contacts)
	}
	if len(contacts[0].URLAddresses) != 0 || contacts[0].Note != "" {
		t.Fatalf("optional values = %#v", contacts[0])
	}
}

func TestReadAddressBookDatabaseReportsMissingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), addressBookDBName)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table ZABCDRECORD (Z_PK integer primary key)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = readAddressBookDatabase(t.Context(), path)
	if err == nil || !strings.Contains(err.Error(), "missing table ZABCDPHONENUMBER") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadAddressBookDatabaseDoesNotCreateLiveSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), addressBookDBName)
	createAddressBookFixture(t, path, []fixtureContact{{
		PK:         1,
		Identifier: "fixture-contact:ABPerson",
		FirstName:  "Ada",
		LastName:   "Example",
		Emails:     []string{"ada@example.com"},
	}})
	setWALModeWithoutSidecars(t, path)

	contacts, err := readAddressBookDatabase(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 1 || contacts[0].Name() != "Ada Example" {
		t.Fatalf("contacts = %#v", contacts)
	}
	assertNoAddressBookSidecars(t, path)
}

type fixtureContact struct {
	PK            int
	Identifier    string
	FirstName     string
	MiddleName    string
	LastName      string
	MaidenName    string
	Prefix        string
	Suffix        string
	Nickname      string
	PhoneticFirst string
	PhoneticLast  string
	Organisation  string
	Department    string
	JobTitle      string
	Birthday      *time.Time
	Note          string
	Emails        []string
	Phones        []string
	Address       fixtureAddress
	URLs          []string
	Social        []fixtureService
	Messaging     []fixtureService
	Dates         []fixtureDate
	Related       []fixtureService
	Avatar        []byte
}

type fixtureService struct {
	Service string
	Value   string
}

type fixtureDate struct {
	Label string
	When  time.Time
}

// appleTimestamp is the Core Data form the address book stores: seconds from
// 2001-01-01 UTC.
func appleTimestamp(when time.Time) float64 {
	return float64(when.UTC().Unix() - appleEpochOffset)
}

type fixtureAddress struct {
	Label       string
	Street      string
	City        string
	State       string
	ZipCode     string
	CountryName string
	CountryCode string
}

func createAddressBookFixture(t *testing.T, path string, contacts []fixtureContact) {
	t.Helper()
	if err := ensureParentDir(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	statements := []string{
		`create table Z_PRIMARYKEY (Z_ENT integer, Z_NAME varchar, Z_SUPER integer)`,
		`insert into Z_PRIMARYKEY (Z_ENT, Z_NAME, Z_SUPER) values (22, 'ABCDContact', 17)`,
		`create table ZABCDRECORD (
			Z_PK integer primary key,
			Z_ENT integer,
			ZFIRSTNAME varchar,
			ZMIDDLENAME varchar,
			ZLASTNAME varchar,
			ZMAIDENNAME varchar,
			ZTITLE varchar,
			ZSUFFIX varchar,
			ZNICKNAME varchar,
			ZPHONETICFIRSTNAME varchar,
			ZPHONETICLASTNAME varchar,
			ZORGANIZATION varchar,
			ZDEPARTMENT varchar,
			ZJOBTITLE varchar,
			ZBIRTHDAY timestamp,
			ZUNIQUEID varchar,
			ZEXTERNALUUID varchar,
			ZTHUMBNAILIMAGEDATA blob
		)`,
		`create table ZABCDNOTE (
			Z_PK integer primary key,
			ZCONTACT integer,
			ZTEXT varchar
		)`,
		`create table ZABCDURLADDRESS (
			Z_PK integer primary key,
			ZOWNER integer,
			ZURL varchar,
			ZLABEL varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDSOCIALPROFILE (
			Z_PK integer primary key,
			ZOWNER integer,
			ZSERVICENAME varchar,
			ZUSERNAME varchar,
			ZURL varchar,
			ZLABEL varchar,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDMESSAGINGADDRESS (
			Z_PK integer primary key,
			ZOWNER integer,
			ZSERVICENAME varchar,
			ZADDRESS varchar,
			ZLABEL varchar,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDDATE (
			Z_PK integer primary key,
			ZOWNER integer,
			ZDATE timestamp,
			ZLABEL varchar,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDRELATEDNAME (
			Z_PK integer primary key,
			ZOWNER integer,
			ZNAME varchar,
			ZLABEL varchar,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDPHONENUMBER (
			Z_PK integer primary key,
			ZOWNER integer,
			Z22_OWNER integer,
			ZFULLNUMBER varchar,
			ZLABEL varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDEMAILADDRESS (
			Z_PK integer primary key,
			ZOWNER integer,
			Z22_OWNER integer,
			ZADDRESS varchar,
			ZLABEL varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
		`create table ZABCDPOSTALADDRESS (
			Z_PK integer primary key,
			ZOWNER integer,
			Z22_OWNER integer,
			ZLABEL varchar,
			ZSTREET varchar,
			ZCITY varchar,
			ZSTATE varchar,
			ZZIPCODE varchar,
			ZCOUNTRYNAME varchar,
			ZCOUNTRYCODE varchar,
			ZISPRIMARY integer,
			ZORDERINGINDEX integer
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, contact := range contacts {
		var birthday any
		if contact.Birthday != nil {
			birthday = appleTimestamp(*contact.Birthday)
		}
		if _, err := db.Exec(`insert into ZABCDRECORD (Z_PK, Z_ENT, ZFIRSTNAME, ZMIDDLENAME, ZLASTNAME, ZMAIDENNAME, ZTITLE, ZSUFFIX, ZNICKNAME, ZPHONETICFIRSTNAME, ZPHONETICLASTNAME, ZORGANIZATION, ZDEPARTMENT, ZJOBTITLE, ZBIRTHDAY, ZUNIQUEID, ZTHUMBNAILIMAGEDATA) values (?, 22, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			contact.PK, contact.FirstName, contact.MiddleName, contact.LastName, contact.MaidenName, contact.Prefix, contact.Suffix,
			contact.Nickname, contact.PhoneticFirst, contact.PhoneticLast, contact.Organisation, contact.Department, contact.JobTitle,
			birthday, contact.Identifier, contact.Avatar); err != nil {
			t.Fatal(err)
		}
		if contact.Note != "" {
			if _, err := db.Exec(`insert into ZABCDNOTE (ZCONTACT, ZTEXT) values (?, ?)`, contact.PK, contact.Note); err != nil {
				t.Fatal(err)
			}
		}
		for i, url := range contact.URLs {
			if _, err := db.Exec(`insert into ZABCDURLADDRESS (ZOWNER, ZURL, ZLABEL, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, '_$!<HomePage>!$_', ?, ?)`,
				contact.PK, url, boolInt(i == 0), i); err != nil {
				t.Fatal(err)
			}
		}
		for i, profile := range contact.Social {
			if _, err := db.Exec(`insert into ZABCDSOCIALPROFILE (ZOWNER, ZSERVICENAME, ZUSERNAME, ZURL, ZORDERINGINDEX) values (?, ?, ?, ?, ?)`,
				contact.PK, profile.Service, profile.Value, "https://example.com/"+profile.Value, i); err != nil {
				t.Fatal(err)
			}
		}
		for i, handle := range contact.Messaging {
			if _, err := db.Exec(`insert into ZABCDMESSAGINGADDRESS (ZOWNER, ZSERVICENAME, ZADDRESS, ZORDERINGINDEX) values (?, ?, ?, ?)`,
				contact.PK, handle.Service, handle.Value, i); err != nil {
				t.Fatal(err)
			}
		}
		for i, date := range contact.Dates {
			if _, err := db.Exec(`insert into ZABCDDATE (ZOWNER, ZDATE, ZLABEL, ZORDERINGINDEX) values (?, ?, ?, ?)`,
				contact.PK, appleTimestamp(date.When), date.Label, i); err != nil {
				t.Fatal(err)
			}
		}
		for i, related := range contact.Related {
			if _, err := db.Exec(`insert into ZABCDRELATEDNAME (ZOWNER, ZNAME, ZLABEL, ZORDERINGINDEX) values (?, ?, ?, ?)`,
				contact.PK, related.Value, related.Service, i); err != nil {
				t.Fatal(err)
			}
		}
		for i, email := range contact.Emails {
			if _, err := db.Exec(`insert into ZABCDEMAILADDRESS (ZOWNER, ZADDRESS, ZLABEL, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, '_$!<Home>!$_', ?, ?)`,
				contact.PK, email, boolInt(i == 0), i); err != nil {
				t.Fatal(err)
			}
		}
		for i, phone := range contact.Phones {
			if _, err := db.Exec(`insert into ZABCDPHONENUMBER (ZOWNER, ZFULLNUMBER, ZLABEL, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, '_$!<Mobile>!$_', ?, ?)`,
				contact.PK, phone, boolInt(i == 0), i); err != nil {
				t.Fatal(err)
			}
		}
		if contact.Address.Street != "" || contact.Address.City != "" {
			if _, err := db.Exec(`insert into ZABCDPOSTALADDRESS (ZOWNER, ZLABEL, ZSTREET, ZCITY, ZSTATE, ZZIPCODE, ZCOUNTRYNAME, ZCOUNTRYCODE, ZISPRIMARY, ZORDERINGINDEX) values (?, ?, ?, ?, ?, ?, ?, ?, 1, 0)`,
				contact.PK, contact.Address.Label, contact.Address.Street, contact.Address.City, contact.Address.State, contact.Address.ZipCode, contact.Address.CountryName, contact.Address.CountryCode); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func setWALModeWithoutSidecars(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma journal_mode=WAL`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma wal_checkpoint(TRUNCATE)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func assertNoAddressBookSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("live sidecar %s stat err = %v, want not exist", path+suffix, err)
		}
	}
}
