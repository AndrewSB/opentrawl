---
written_by: ai
---

# Contacts

Contacts is OpenTrawl's People index. It stores people and identifiers in its
own local archive:

```text
~/.opentrawl/contacts/contacts.db
```

The SQLite archive groups source identities from Apple Contacts and messaging
archives without flattening their original records. Strong identifiers such as
phone numbers, email addresses and source accounts connect identities. The
grouping link can be changed without deleting the source records, and user
annotations survive later syncs.

## Sync

Normal OpenTrawl sync reads Apple Contacts automatically and creates or updates
the People archive:

```sh
trawl sync contacts
```

Later source snapshots replace only that source's values. Values from other
sources and user annotations remain intact.

## The contact card

A sync keeps the whole card a source states, in the vCard field names Apple
Contacts, Google Contacts and CardDAV share, rather than flattening it into a
display name:

- names: given, middle and family name, maiden name, prefix and suffix,
  nickname, and the phonetic spellings
- work: organisation, department and job title
- birthday, and any other labelled date such as an anniversary
- websites, social profiles, instant message addresses and related names, each
  keeping the label or service name the source attached to it
- the note on the card

Nicknames, maiden names, phonetic spellings and organisations are searchable
aliases, so `trawl contacts search` finds a person by any of them. Emails and
phone numbers keep their own labels; a custom label is kept as it was typed.
A card with a name but no phone or email is a contact and is kept.

OpenTrawl does not import contact photos yet.

## Commands

```sh
trawl contacts status
trawl contacts search Ada
trawl contacts who Ada
trawl contacts person list
trawl contacts person show ada@example.com
trawl contacts person annotate person_123 "Ada is the project accountant"
```

Use the normal text output for people and agents. Add `--json` only for scripts.
OpenTrawl never writes back to Apple Contacts or another address book.

The archive contains private contact and annotation data. Public fixtures use
invented people, `example.com` addresses and `+1555` phone numbers.
