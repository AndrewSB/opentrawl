//go:build darwin

package apple

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit/cache"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	addressBookDBName = "AddressBook-v22.abcddb"
	// Core Data counts seconds from 2001-01-01 UTC.
	appleEpochOffset = 978307200
	// Apple records a birthday with no year in year 1604.
	yearlessDateYear = 1604
)

type addressBookAccessError struct {
	Path string
	Err  error
}

type invalidSchemaError struct {
	Err error
}

func (e invalidSchemaError) Error() string {
	return e.Err.Error()
}

func (e invalidSchemaError) Unwrap() error {
	return e.Err
}

func invalidSchema(err error) error {
	return invalidSchemaError{Err: err}
}

func (e addressBookAccessError) Error() string {
	return fmt.Sprintf("read Apple AddressBook database %s: %v. Grant Full Disk Access to OpenTrawl or the terminal running it in System Settings > Privacy & Security > Full Disk Access, then retry", e.Path, e.Err)
}

func (e addressBookAccessError) Unwrap() error {
	return e.Err
}

type addressBookSchema struct {
	contactEntity  int64
	recordColumns  map[string]bool
	phoneColumns   map[string]bool
	emailColumns   map[string]bool
	postalColumns  map[string]bool
	noteColumns    map[string]bool
	urlColumns     map[string]bool
	socialColumns  map[string]bool
	messageColumns map[string]bool
	dateColumns    map[string]bool
	relatedColumns map[string]bool
}

type addressBookRecord struct {
	contact  Contact
	phones   []LabeledValue
	emails   []LabeledValue
	postal   []LabeledValue
	urls     []LabeledValue
	social   []LabeledValue
	messages []LabeledValue
	dates    []LabeledValue
	related  []LabeledValue
}

// cardColumns maps the single-valued card columns to their vCard field. Every
// one is read through optionalTextExpression, so an address book that predates
// a column simply leaves that field blank.
var cardColumns = []struct {
	column string
	assign func(*model.Card, string)
}{
	{"ZFIRSTNAME", func(c *model.Card, v string) { c.GivenName = v }},
	{"ZMIDDLENAME", func(c *model.Card, v string) { c.MiddleName = v }},
	{"ZLASTNAME", func(c *model.Card, v string) { c.FamilyName = v }},
	{"ZMAIDENNAME", func(c *model.Card, v string) { c.PreviousFamilyName = v }},
	{"ZTITLE", func(c *model.Card, v string) { c.NamePrefix = v }},
	{"ZSUFFIX", func(c *model.Card, v string) { c.NameSuffix = v }},
	{"ZNICKNAME", func(c *model.Card, v string) { c.Nickname = v }},
	{"ZPHONETICFIRSTNAME", func(c *model.Card, v string) { c.PhoneticGivenName = v }},
	{"ZPHONETICMIDDLENAME", func(c *model.Card, v string) { c.PhoneticMiddleName = v }},
	{"ZPHONETICLASTNAME", func(c *model.Card, v string) { c.PhoneticFamilyName = v }},
	{"ZPHONETICORGANIZATION", func(c *model.Card, v string) { c.PhoneticOrganizationName = v }},
	{"ZORGANIZATION", func(c *model.Card, v string) { c.OrganizationName = v }},
	{"ZDEPARTMENT", func(c *model.Card, v string) { c.DepartmentName = v }},
	{"ZJOBTITLE", func(c *model.Card, v string) { c.JobTitle = v }},
}

func ReadSystem(ctx context.Context) ([]Contact, error) {
	// Apple Contacts has no trawlkit crawler contract in this tree. This
	// explicit import path reads the local AddressBook database read-only.
	dir, err := addressBookDir()
	if err != nil {
		return nil, err
	}
	return readAddressBookDir(ctx, dir)
}

func CheckSource(ctx context.Context) (SourceState, error) {
	dir, err := addressBookDir()
	if err != nil {
		return sourceStateForError(err), err
	}
	paths, err := addressBookDatabasePaths(dir)
	if err != nil {
		return sourceStateForError(err), err
	}
	for _, path := range paths {
		if err := checkAddressBookDatabase(ctx, path); err != nil {
			return sourceStateForError(err), err
		}
	}
	return SourceReady, nil
}

func checkSourceAt(ctx context.Context, dir string) (SourceState, error) {
	paths, err := addressBookDatabasePaths(dir)
	if err != nil {
		return sourceStateForError(err), err
	}
	for _, path := range paths {
		if err := checkAddressBookDatabase(ctx, path); err != nil {
			return sourceStateForError(err), err
		}
	}
	return SourceReady, nil
}

func addressBookDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "AddressBook"), nil
}

func readAddressBookDir(ctx context.Context, dir string) ([]Contact, error) {
	paths, err := addressBookDatabasePaths(dir)
	if err != nil {
		return nil, err
	}
	contacts := make([]Contact, 0)
	contactIndex := map[string]int{}
	for _, path := range paths {
		dbContacts, err := readAddressBookDatabase(ctx, path)
		if err != nil {
			return nil, err
		}
		for _, contact := range dbContacts {
			if strings.TrimSpace(contact.Identifier) == "" {
				continue
			}
			if index, ok := contactIndex[contact.Identifier]; ok {
				contacts[index] = mergeContact(contacts[index], contact)
				continue
			}
			contactIndex[contact.Identifier] = len(contacts)
			contacts = append(contacts, contact)
		}
	}
	return contacts, nil
}

func addressBookDatabasePaths(dir string) ([]string, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, addressBookAccessError{Path: dir, Err: err}
	}

	var paths []string
	rootDB := filepath.Join(dir, addressBookDBName)
	if info, err := os.Stat(rootDB); err == nil && !info.IsDir() {
		paths = append(paths, rootDB)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, addressBookAccessError{Path: rootDB, Err: err}
	}

	sourcesDir := filepath.Join(dir, "Sources")
	entries, err := os.ReadDir(sourcesDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, addressBookAccessError{Path: sourcesDir, Err: err}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(sourcesDir, entry.Name(), addressBookDBName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			paths = append(paths, path)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, addressBookAccessError{Path: path, Err: err}
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("read Apple AddressBook: no %s files found under %s", addressBookDBName, dir)
	}
	return paths, nil
}

func readAddressBookDatabase(ctx context.Context, path string) ([]Contact, error) {
	snapshotDir, err := os.MkdirTemp("", "contacts-addressbook-")
	if err != nil {
		return nil, addressBookAccessError{Path: path, Err: err}
	}
	defer func() { _ = os.RemoveAll(snapshotDir) }()
	snapshot, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     path,
		DestinationDir: snapshotDir,
		Name:           filepath.Base(path),
	})
	if err != nil {
		return nil, addressBookAccessError{Path: path, Err: err}
	}
	st, err := ckstore.OpenForeignReadOnly(ctx, snapshot.Path)
	if err != nil {
		return nil, addressBookAccessError{Path: path, Err: err}
	}
	defer func() { _ = st.Close() }()
	db := st.DB()

	schema, err := inspectAddressBookSchema(ctx, db, path)
	if err != nil {
		return nil, err
	}
	records, order, err := readAddressBookRecords(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	if err := readAddressBookPhones(ctx, db, schema, records); err != nil {
		return nil, err
	}
	if err := readAddressBookEmails(ctx, db, schema, records); err != nil {
		return nil, err
	}
	if err := readAddressBookPostalAddresses(ctx, db, schema, records); err != nil {
		return nil, err
	}
	if err := readAddressBookCardValues(ctx, db, schema, records); err != nil {
		return nil, err
	}

	contacts := make([]Contact, 0, len(records))
	for _, pk := range order {
		record := records[pk]
		// A card with a name is a contact. Apple keeps name-only and
		// organisation-only cards, so requiring a phone or an email here would
		// drop them from the archive entirely.
		if strings.TrimSpace(record.contact.Name()) == "" {
			continue
		}
		record.contact.Emails = record.emails
		record.contact.Phones = record.phones
		record.contact.Addresses = record.postal
		record.contact.URLAddresses = record.urls
		record.contact.SocialProfiles = record.social
		record.contact.InstantMessages = record.messages
		record.contact.Dates = record.dates
		record.contact.Relations = record.related
		contacts = append(contacts, record.contact)
	}
	return contacts, nil
}

// readAddressBookCardValues reads the repeated card fields that live in their
// own tables. Each table is optional: an address book that does not have one
// contributes nothing rather than failing the whole read.
func readAddressBookCardValues(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	if err := readAddressBookNotes(ctx, db, schema, records); err != nil {
		return err
	}
	for _, table := range []struct {
		name    string
		columns map[string]bool
		value   string
		service string
		date    bool
		assign  func(*addressBookRecord, LabeledValue)
	}{
		{name: "ZABCDURLADDRESS", columns: schema.urlColumns, value: "ZURL",
			assign: func(r *addressBookRecord, v LabeledValue) { r.urls = appendUniqueLabeledValue(r.urls, v) }},
		{name: "ZABCDSOCIALPROFILE", columns: schema.socialColumns, value: "ZUSERNAME", service: "ZSERVICENAME",
			assign: func(r *addressBookRecord, v LabeledValue) { r.social = appendUniqueLabeledValue(r.social, v) }},
		{name: "ZABCDMESSAGINGADDRESS", columns: schema.messageColumns, value: "ZADDRESS", service: "ZSERVICENAME",
			assign: func(r *addressBookRecord, v LabeledValue) { r.messages = appendUniqueLabeledValue(r.messages, v) }},
		{name: "ZABCDDATE", columns: schema.dateColumns, value: "ZDATE", date: true,
			assign: func(r *addressBookRecord, v LabeledValue) { r.dates = appendUniqueLabeledValue(r.dates, v) }},
		{name: "ZABCDRELATEDNAME", columns: schema.relatedColumns, value: "ZNAME",
			assign: func(r *addressBookRecord, v LabeledValue) { r.related = appendUniqueLabeledValue(r.related, v) }},
	} {
		if !table.columns[table.value] {
			continue
		}
		owner, err := ownerExpression(table.columns)
		if err != nil {
			continue
		}
		// A social profile without a username, and any profile at all, still
		// has a URL worth keeping.
		valueExpr := "coalesce(nullif(" + table.value + ", ''), " + optionalTextExpression(table.columns, "ZURL") + ")"
		if table.date {
			valueExpr = optionalNumberExpression(table.columns, table.value)
		}
		query := fmt.Sprintf(`select %s, %s, %s, %s from %s order by %s`,
			owner, valueExpr,
			optionalTextExpression(table.columns, "ZLABEL"),
			optionalTextExpression(table.columns, table.service),
			table.name, orderingExpression(table.columns))
		if err := readCardValueRows(ctx, db, query, table.date, func(owner int64, value LabeledValue) {
			if record := records[owner]; record != nil {
				table.assign(record, value)
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

// readCardValueRows reads one repeated card table. The service name a card
// states for a social or messaging handle is the label a reader wants, so it
// wins over Apple's generic label when both are present.
func readCardValueRows(ctx context.Context, db *sql.DB, query string, date bool, add func(int64, LabeledValue)) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner int64
		var raw sql.NullString
		var label, service string
		if err := rows.Scan(&owner, &raw, &label, &service); err != nil {
			return err
		}
		value := strings.TrimSpace(raw.String)
		if date {
			value = appleDateText(parseAppleNumber(raw))
		}
		if owner == 0 || value == "" {
			continue
		}
		if service = strings.TrimSpace(service); service != "" {
			label = service
		}
		add(owner, LabeledValue{Value: value, Label: label})
	}
	return rows.Err()
}

func parseAppleNumber(value sql.NullString) sql.NullFloat64 {
	if !value.Valid {
		return sql.NullFloat64{}
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value.String), 64)
	if err != nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: seconds, Valid: true}
}

func readAddressBookNotes(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	if !schema.noteColumns["ZTEXT"] {
		return nil
	}
	owner, err := ownerExpression(schema.noteColumns, "ZCONTACT")
	if err != nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`select %s, coalesce(ZTEXT, '') from ZABCDNOTE`, owner))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner int64
		var text string
		if err := rows.Scan(&owner, &text); err != nil {
			return err
		}
		if record := records[owner]; record != nil && strings.TrimSpace(text) != "" {
			record.contact.Note = strings.TrimSpace(text)
		}
	}
	return rows.Err()
}

// appleDateText formats a Core Data timestamp, which counts seconds from
// 2001-01-01 UTC. Apple stores a birthday whose year the card omits in year
// 1604, and a reader must not report that as a real year.
func appleDateText(value sql.NullFloat64) string {
	if !value.Valid {
		return ""
	}
	moment := time.Unix(int64(value.Float64)+appleEpochOffset, 0).UTC()
	if moment.Year() == yearlessDateYear {
		return moment.Format("--01-02")
	}
	return moment.Format("2006-01-02")
}

func checkAddressBookDatabase(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		if isSourcePermissionError(err) {
			return addressBookAccessError{Path: path, Err: err}
		}
		return err
	}
	_ = file.Close()

	st, err := ckstore.OpenForeignReadOnly(ctx, path)
	if err != nil {
		if isSourcePermissionError(err) {
			return addressBookAccessError{Path: path, Err: err}
		}
		if isMalformedSQLiteError(err) {
			return invalidSourceError{Path: path, Err: err}
		}
		return err
	}
	defer func() { _ = st.Close() }()
	_, err = inspectAddressBookSchema(ctx, st.DB(), path)
	if err != nil {
		var schemaErr invalidSchemaError
		if errors.As(err, &schemaErr) {
			return invalidSourceError{Path: path, Err: err}
		}
		return err
	}
	return err
}

func isMalformedSQLiteError(err error) bool {
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code {
	case sqlite3.ErrNotADB, sqlite3.ErrCorrupt, sqlite3.ErrFormat:
		return true
	default:
		return false
	}
}

func inspectAddressBookSchema(ctx context.Context, db *sql.DB, path string) (addressBookSchema, error) {
	schema := addressBookSchema{}
	var err error
	schema.recordColumns, err = tableColumns(ctx, db, "ZABCDRECORD")
	if err != nil {
		return schema, err
	}
	schema.phoneColumns, err = tableColumns(ctx, db, "ZABCDPHONENUMBER")
	if err != nil {
		return schema, err
	}
	schema.emailColumns, err = tableColumns(ctx, db, "ZABCDEMAILADDRESS")
	if err != nil {
		return schema, err
	}
	schema.postalColumns, err = tableColumns(ctx, db, "ZABCDPOSTALADDRESS")
	if err != nil {
		return schema, err
	}
	// The repeated card tables are optional. Their absence costs those fields,
	// not the read.
	schema.noteColumns = optionalTableColumns(ctx, db, "ZABCDNOTE")
	schema.urlColumns = optionalTableColumns(ctx, db, "ZABCDURLADDRESS")
	schema.socialColumns = optionalTableColumns(ctx, db, "ZABCDSOCIALPROFILE")
	schema.messageColumns = optionalTableColumns(ctx, db, "ZABCDMESSAGINGADDRESS")
	schema.dateColumns = optionalTableColumns(ctx, db, "ZABCDDATE")
	schema.relatedColumns = optionalTableColumns(ctx, db, "ZABCDRELATEDNAME")
	primaryKeyColumns, err := tableColumns(ctx, db, "Z_PRIMARYKEY")
	if err != nil {
		return schema, err
	}
	for _, column := range []string{"Z_ENT", "Z_NAME"} {
		if !primaryKeyColumns[column] {
			return schema, unrecognisedAddressBookLayout(path, "Z_PRIMARYKEY", column)
		}
	}
	for _, column := range []string{"Z_PK", "Z_ENT", "ZFIRSTNAME", "ZLASTNAME", "ZORGANIZATION"} {
		if !schema.recordColumns[column] {
			return schema, unrecognisedAddressBookLayout(path, "ZABCDRECORD", column)
		}
	}
	if !schema.recordColumns["ZUNIQUEID"] && !schema.recordColumns["ZEXTERNALUUID"] && !schema.recordColumns["ZEXTERNALIDENTIFIER"] {
		return schema, invalidSchema(fmt.Errorf("unrecognised AddressBook database layout in %s: missing identifier column in ZABCDRECORD", path))
	}
	for _, table := range []struct {
		name    string
		columns map[string]bool
		value   string
	}{
		{name: "ZABCDPHONENUMBER", columns: schema.phoneColumns, value: "ZFULLNUMBER"},
		{name: "ZABCDEMAILADDRESS", columns: schema.emailColumns, value: "ZADDRESS"},
	} {
		if !table.columns[table.value] {
			return schema, unrecognisedAddressBookLayout(path, table.name, table.value)
		}
		if _, err := ownerExpression(table.columns); err != nil {
			return schema, invalidSchema(fmt.Errorf("unrecognised AddressBook database layout in %s: missing owner column in %s", path, table.name))
		}
	}
	if _, err := ownerExpression(schema.postalColumns); err != nil {
		return schema, invalidSchema(fmt.Errorf("unrecognised AddressBook database layout in %s: missing owner column in ZABCDPOSTALADDRESS", path))
	}
	if !hasAnyColumn(schema.postalColumns, "ZSTREET", "ZCITY", "ZZIPCODE", "ZCOUNTRYNAME", "ZCOUNTRYCODE") {
		return schema, invalidSchema(fmt.Errorf("unrecognised AddressBook database layout in %s: missing postal address columns in ZABCDPOSTALADDRESS", path))
	}

	if err := db.QueryRowContext(ctx, `select Z_ENT from Z_PRIMARYKEY where Z_NAME = 'ABCDContact'`).Scan(&schema.contactEntity); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schema, invalidSchema(fmt.Errorf("unrecognised AddressBook database layout in %s: missing ABCDContact entity in Z_PRIMARYKEY", path))
		}
		return schema, err
	}
	return schema, nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "pragma table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, invalidSchema(fmt.Errorf("unrecognised AddressBook database layout: missing table %s", table))
	}
	return columns, nil
}

func optionalTableColumns(ctx context.Context, db *sql.DB, table string) map[string]bool {
	columns, err := tableColumns(ctx, db, table)
	if err != nil {
		return nil
	}
	return columns
}

func unrecognisedAddressBookLayout(path, table, column string) error {
	return invalidSchema(fmt.Errorf("unrecognised AddressBook database layout in %s: missing column %s.%s", path, table, column))
}

func readAddressBookRecords(ctx context.Context, db *sql.DB, schema addressBookSchema) (map[int64]*addressBookRecord, []int64, error) {
	idExpr := firstPresentExpression(schema.recordColumns, "ZUNIQUEID", "ZEXTERNALUUID", "ZEXTERNALIDENTIFIER")
	avatarExpr := "null"
	if schema.recordColumns["ZTHUMBNAILIMAGEDATA"] {
		avatarExpr = "ZTHUMBNAILIMAGEDATA"
	}
	expressions := []string{"Z_PK", idExpr, avatarExpr, optionalNumberExpression(schema.recordColumns, "ZBIRTHDAY")}
	for _, column := range cardColumns {
		expressions = append(expressions, optionalTextExpression(schema.recordColumns, column.column))
	}
	query := fmt.Sprintf(`
select %s
from ZABCDRECORD
where Z_ENT = ?
order by Z_PK`, strings.Join(expressions, ", "))
	rows, err := db.QueryContext(ctx, query, schema.contactEntity)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	records := map[int64]*addressBookRecord{}
	var order []int64
	for rows.Next() {
		var pk int64
		var identifier string
		var avatar []byte
		var birthday sql.NullFloat64
		card := make([]string, len(cardColumns))
		targets := []any{&pk, &identifier, &avatar, &birthday}
		for i := range card {
			targets = append(targets, &card[i])
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, nil, err
		}
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			continue
		}
		contact := Contact{Identifier: identifier, AvatarData: append([]byte(nil), avatar...)}
		for i, column := range cardColumns {
			column.assign(&contact.Card, strings.TrimSpace(card[i]))
		}
		contact.Birthday = appleDateText(birthday)
		contact.FullName = fullName(contact.Card)
		records[pk] = &addressBookRecord{contact: contact}
		order = append(order, pk)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return records, order, nil
}

func readAddressBookPhones(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	owner, err := ownerExpression(schema.phoneColumns)
	if err != nil {
		return err
	}
	labelExpr := optionalTextExpression(schema.phoneColumns, "ZLABEL")
	orderExpr := orderingExpression(schema.phoneColumns)
	query := fmt.Sprintf(`
select %s, coalesce(ZFULLNUMBER, ''), %s
from ZABCDPHONENUMBER
where trim(coalesce(ZFULLNUMBER, '')) <> ''
order by %s`, owner, labelExpr, orderExpr)
	return readLabeledValues(ctx, db, query, func(owner int64, value LabeledValue) {
		if record := records[owner]; record != nil {
			record.phones = appendUniqueLabeledValue(record.phones, value)
		}
	})
}

func readAddressBookEmails(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	owner, err := ownerExpression(schema.emailColumns)
	if err != nil {
		return err
	}
	labelExpr := optionalTextExpression(schema.emailColumns, "ZLABEL")
	orderExpr := orderingExpression(schema.emailColumns)
	query := fmt.Sprintf(`
select %s, coalesce(ZADDRESS, ''), %s
from ZABCDEMAILADDRESS
where trim(coalesce(ZADDRESS, '')) <> ''
order by %s`, owner, labelExpr, orderExpr)
	return readLabeledValues(ctx, db, query, func(owner int64, value LabeledValue) {
		if record := records[owner]; record != nil {
			record.emails = appendUniqueLabeledValue(record.emails, value)
		}
	})
}

func readAddressBookPostalAddresses(ctx context.Context, db *sql.DB, schema addressBookSchema, records map[int64]*addressBookRecord) error {
	owner, err := ownerExpression(schema.postalColumns)
	if err != nil {
		return err
	}
	labelExpr := optionalTextExpression(schema.postalColumns, "ZLABEL")
	orderExpr := orderingExpression(schema.postalColumns)
	query := fmt.Sprintf(`
select %s, %s, %s, %s, %s, %s, %s, %s
from ZABCDPOSTALADDRESS
order by %s`,
		owner,
		labelExpr,
		optionalTextExpression(schema.postalColumns, "ZSTREET"),
		optionalTextExpression(schema.postalColumns, "ZCITY"),
		optionalTextExpression(schema.postalColumns, "ZSTATE"),
		optionalTextExpression(schema.postalColumns, "ZZIPCODE"),
		optionalTextExpression(schema.postalColumns, "ZCOUNTRYNAME"),
		optionalTextExpression(schema.postalColumns, "ZCOUNTRYCODE"),
		orderExpr,
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner int64
		var label, street, city, state, zipCode, countryName, countryCode string
		if err := rows.Scan(&owner, &label, &street, &city, &state, &zipCode, &countryName, &countryCode); err != nil {
			return err
		}
		value := postalAddressValue(street, city, state, zipCode, countryName, countryCode)
		if value == "" {
			continue
		}
		if record := records[owner]; record != nil {
			record.postal = appendUniquePostalAddress(record.postal, LabeledValue{Value: value, Label: label})
		}
	}
	return rows.Err()
}

func readLabeledValues(ctx context.Context, db *sql.DB, query string, add func(int64, LabeledValue)) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner int64
		var value, label string
		if err := rows.Scan(&owner, &value, &label); err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if owner == 0 || value == "" {
			continue
		}
		add(owner, LabeledValue{Value: value, Label: label})
	}
	return rows.Err()
}

func firstPresentExpression(columns map[string]bool, names ...string) string {
	var expressions []string
	for _, name := range names {
		if columns[name] {
			expressions = append(expressions, "nullif("+name+", '')")
		}
	}
	if len(expressions) == 1 {
		return "coalesce(" + expressions[0] + ", '')"
	}
	return "coalesce(" + strings.Join(expressions, ", ") + ", '')"
}

func optionalTextExpression(columns map[string]bool, name string) string {
	if columns[name] {
		return "coalesce(" + name + ", '')"
	}
	return "''"
}

// optionalNumberExpression casts so that the driver reports the raw Core Data
// number rather than converting a column declared as a timestamp.
func optionalNumberExpression(columns map[string]bool, name string) string {
	if columns[name] {
		return "cast(" + name + " as real)"
	}
	return "null"
}

// ownerExpression finds the column linking a repeated value back to its
// record. Core Data names it per entity version, and notes link through
// ZCONTACT instead, so callers pass the names they also accept.
func ownerExpression(columns map[string]bool, preferred ...string) (string, error) {
	var expressions []string
	for _, name := range preferred {
		if columns[name] {
			expressions = append(expressions, "nullif("+name+", 0)")
		}
	}
	if columns["ZOWNER"] {
		expressions = append(expressions, "nullif(ZOWNER, 0)")
	}
	var versioned []string
	for column := range columns {
		if strings.HasPrefix(column, "Z") && strings.HasSuffix(column, "_OWNER") && column != "ZOWNER" {
			versioned = append(versioned, column)
		}
	}
	sort.Strings(versioned)
	for _, column := range versioned {
		expressions = append(expressions, "nullif("+column+", 0)")
	}
	if len(expressions) == 0 {
		return "", fmt.Errorf("missing owner column")
	}
	if len(expressions) == 1 {
		return "coalesce(" + expressions[0] + ", 0)", nil
	}
	return "coalesce(" + strings.Join(expressions, ", ") + ", 0)", nil
}

func orderingExpression(columns map[string]bool) string {
	var terms []string
	if columns["ZISPRIMARY"] {
		terms = append(terms, "coalesce(ZISPRIMARY, 0) desc")
	}
	if columns["ZORDERINGINDEX"] {
		terms = append(terms, "coalesce(ZORDERINGINDEX, 0)")
	}
	if columns["Z_PK"] {
		terms = append(terms, "Z_PK")
	}
	if len(terms) == 0 {
		return "1"
	}
	return strings.Join(terms, ", ")
}

func hasAnyColumn(columns map[string]bool, names ...string) bool {
	for _, name := range names {
		if columns[name] {
			return true
		}
	}
	return false
}

// fullName is the display name only. Every part it draws on is also kept as
// its own card field, so nothing here is the sole record of a value.
func fullName(card model.Card) string {
	if name := strings.Join(nonEmptyStrings(card.GivenName, card.MiddleName, card.FamilyName), " "); name != "" {
		return name
	}
	if card.OrganizationName != "" {
		return strings.TrimSpace(card.OrganizationName)
	}
	return strings.TrimSpace(card.Nickname)
}

func postalAddressValue(street, city, state, zipCode, countryName, countryCode string) string {
	street = strings.TrimSpace(street)
	city = strings.TrimSpace(city)
	state = strings.TrimSpace(state)
	zipCode = strings.TrimSpace(zipCode)
	countryName = strings.TrimSpace(countryName)
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))

	var locality string
	switch {
	case state != "" && city != "":
		locality = strings.Join(nonEmptyStrings(city, state, zipCode), " ")
	case zipCode != "" && city != "":
		locality = strings.Join(nonEmptyStrings(zipCode, city), " ")
	default:
		locality = strings.Join(nonEmptyStrings(city, state, zipCode), " ")
	}
	if countryName == "" {
		countryName = countryCode
	}
	return strings.Join(nonEmptyStrings(street, locality, countryName), "\n")
}

// mergeContact folds the same contact seen in two address book databases into
// one card. The first database to state a field owns it.
func mergeContact(base, incoming Contact) Contact {
	base.Card = base.Fill(incoming.Card)
	if strings.TrimSpace(base.FullName) == "" {
		base.FullName = incoming.FullName
	}
	base.Emails = appendUniqueLabeledValues(base.Emails, incoming.Emails)
	base.Phones = appendUniqueLabeledValues(base.Phones, incoming.Phones)
	base.URLAddresses = appendUniqueLabeledValues(base.URLAddresses, incoming.URLAddresses)
	base.SocialProfiles = appendUniqueLabeledValues(base.SocialProfiles, incoming.SocialProfiles)
	base.InstantMessages = appendUniqueLabeledValues(base.InstantMessages, incoming.InstantMessages)
	base.Dates = appendUniqueLabeledValues(base.Dates, incoming.Dates)
	base.Relations = appendUniqueLabeledValues(base.Relations, incoming.Relations)
	for _, address := range incoming.Addresses {
		base.Addresses = appendUniquePostalAddress(base.Addresses, address)
	}
	if len(base.AvatarData) == 0 && len(incoming.AvatarData) > 0 {
		base.AvatarData = append([]byte(nil), incoming.AvatarData...)
	}
	return base
}

func appendUniqueLabeledValues(values []LabeledValue, incoming []LabeledValue) []LabeledValue {
	for _, value := range incoming {
		values = appendUniqueLabeledValue(values, value)
	}
	return values
}

func appendUniqueLabeledValue(values []LabeledValue, incoming LabeledValue) []LabeledValue {
	key := strings.ToLower(strings.TrimSpace(incoming.Value))
	if key == "" {
		return values
	}
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value.Value)) == key {
			return values
		}
	}
	return append(values, LabeledValue{Value: strings.TrimSpace(incoming.Value), Label: strings.TrimSpace(incoming.Label)})
}

func appendUniquePostalAddress(values []LabeledValue, incoming LabeledValue) []LabeledValue {
	incoming.Value = strings.TrimSpace(incoming.Value)
	incoming.Label = strings.TrimSpace(incoming.Label)
	if incoming.Value == "" {
		return values
	}
	key := strings.ToLower(incoming.Value + "\x00" + incoming.Label)
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value.Value)+"\x00"+strings.TrimSpace(value.Label)) == key {
			return values
		}
	}
	return append(values, incoming)
}
