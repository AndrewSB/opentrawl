---
written_by: ai
status: blocked
blocked_on: a second Mac signed into the same Apple Account
---

# iMessage GUID audit

**This audit cannot run yet.** It compares two Macs independently synchronised
from one Apple Account, and only one Mac is available. Nothing here is
implemented. The design is written out in full so the audit can be built and
run unchanged once a second Mac exists, without reconstructing the reasoning.

This is step 2 of the stable reference identity plan. Step 1 (preserving short
references across snapshot replacement) has landed.

## Why this exists

iMessage references are derived from local SQLite row IDs. Row IDs are a
machine-local ordering detail: two Macs holding the same message assign it
different numbers, and a restore or migration can renumber rows on one machine.
A reference built on them cannot promise to name the same record over time or
across devices.

Apple exposes a second identifier, the message GUID, which looks stable and
looks portable. Before OpenTrawl makes GUIDs the identity authority, that
appearance has to be measured. Apple's Messages database is not a public API,
so observed behaviour is the only evidence available, and it must be gathered
before the canonical reference scheme is fixed rather than after.

The audit answers one question: **does the same provider record carry the same
GUID on two independently synchronised Macs, and does a GUID ever name two
different records?**

## Unblocking conditions

All of these must hold before the audit produces a meaningful result:

- two Macs signed into the same Apple Account;
- Messages in iCloud enabled on both, and both finished syncing;
- overlapping history, including messages predating the second Mac's setup;
- Full Disk Access granted to whatever runs the collector on both machines; and
- the fixture matrix below exercised on both.

A run against two Macs that have not both finished syncing measures iCloud
propagation, not identity stability, and must be discarded.

## Non-goals

- It changes no crawler behaviour and writes nothing to any archive.
- It never writes to `chat.db`; it reads a snapshot copy.
- It does not choose the canonical token encoding. That is step 3, and it
  depends on this result.

## What is collected

The collector reads Apple's `chat.db` directly rather than the OpenTrawl
archive. The archive records only whether a message *has* attachments, not how
many, holds no edit or retraction state, and is one transformation removed from
the provider. The subject here is Apple's behaviour, so the source is Apple's
own store.

Per message row:

| field | source | notes |
|---|---|---|
| `local_rowid` | `message.ROWID` | machine-local; never compared across machines, kept only so an operator can find the record on their own Mac |
| `guid_raw` | `message.guid` | verbatim |
| `guid_normalized` | derived | see normalisation below |
| `guid_normalized_changed` | derived | true when normalisation altered the value |
| `sent_at_utc` | `message.date` | see timestamp normalisation below |
| `date_raw` | `message.date` | the integer as stored, so an interpretation bug is detectable after the fact |
| `direction` | `message.is_from_me` | `sent` or `received` |
| `service` | `message.service` | `iMessage`, `SMS`, `RCS`, … verbatim |
| `attachment_count` | `count(message_attachment_join)` | a count, not a flag |
| `text_digest` | derived | see digest below |
| `edited_at` | `message.date_edited` | optional column |
| `retracted_at` | `message.date_retracted` | optional column |
| `associated_guid` | `message.associated_message_guid` | optional; reactions point at their target |
| `associated_type` | `message.associated_message_type` | optional |
| `chat_guids` | `chat.guid` via `chat_message_join` | sorted; chat GUIDs are themselves candidate stable identifiers |

Optional columns must be probed with a `pragma table_info` check, following the
existing `tableHasColumn` precedent in
`trawlers/imessage/internal/messages/query_files.go`. A missing column is
recorded as absent in the artifact header. It is never silently defaulted to a
zero value, because "not edited" and "this macOS cannot say" are different
facts and conflating them would corrupt the comparison.

### GUID normalisation

Apple GUIDs appear both as bare UUIDs and with prefixes such as `p:0/`.
Normalisation is deliberately minimal: trim surrounding whitespace, apply
Unicode NFC, and upper-case the hexadecimal portion while preserving any
prefix verbatim.

`guid_normalized_changed` records whether normalisation altered the value, so
the audit measures whether normalisation mattered at all rather than assuming
it. Two distinct raw GUIDs are never merged; if normalisation would collapse
them, that is itself a finding and must be reported, not applied.

### Timestamp normalisation

`message.date` is nanoseconds since 2001-01-01 UTC on modern macOS and seconds
since the same epoch in older databases. The crawler assumes nanoseconds
(`AppleDateTime` in `trawlers/imessage/internal/archive/util.go`).

The collector detects the interpretation by magnitude, converts to UTC RFC 3339
at millisecond precision, records which interpretation it used, and also keeps
the raw integer. Keeping both means a wrong guess is recoverable from an
artifact already collected, instead of requiring a second Mac a second time.

### Content digest

```text
text_digest = first 128 bits of SHA-256("opentrawl-guid-audit-v1\0" + normalized_text)
```

`normalized_text` is the message text under Unicode NFC with trailing
whitespace removed, and the empty string when the row has no text. Only the
digest is ever written; the text is not retained.

The digest exists to recognise the same message under two different GUIDs. It
is the only way to tell "iCloud assigned a second GUID to one message" from
"these are two genuinely different messages".

**Recorded residual risk.** The digest is unkeyed, by decision. A bounded
digest of short text is recoverable by dictionary attack: an attacker holding
the artifact can confirm that a message read `ok` or `see you at 6` by hashing
candidates. The artifact must therefore be treated as sensitive — kept on the
two Macs, never committed, never pasted into an issue or a chat. A keyed HMAC
over an operator-supplied passphrase would remove this exposure at the cost of
coordinating one secret across the two machines; it was considered and not
adopted.

## Artifact format

JSON Lines. The first line is a header object; every subsequent line is one
message record, sorted by `(guid_normalized, local_rowid)` so two artifacts
diff deterministically.

Header fields: `schema_version`, `machine_label`, `collected_at`,
`macos_version`, `chatdb_bytes`, `chatdb_modified_at`, `message_count`,
`optional_columns_present`, `digest_scheme`, `timestamp_interpretation`.

The artifact never contains message text, attachment bytes or filenames,
handles, phone numbers, email addresses, display names, or account
identifiers.

It does contain message and chat GUIDs, necessarily — they are the subject of
the audit. Those are provider identifiers, which is the second reason the
artifact is local-only.

## Comparison and classification

The compare step takes two artifacts and emits a report. Every record falls in
exactly one class:

| class | meaning |
|---|---|
| `same_guid_match` | same normalised GUID on both Macs, all mechanical facts equal |
| `same_guid_conflict` | same normalised GUID, differing facts — **the finding that blocks GUID authority** |
| `same_guid_timestamp_skew` | same GUID, all facts equal except the timestamp |
| `equivalent_different_guid` | facts equal including digest, GUIDs differ |
| `missing_guid` | GUID absent or empty on one or both Macs |
| `one_device_only` | GUID present on exactly one Mac |

"Facts equal" means exact equality on direction, service, attachment count and
text digest, with the timestamp equal to the millisecond.

Timestamp skew is separated from outright conflict deliberately. A clock or
timezone handling difference is a plausible benign divergence, and folding it
into `same_guid_conflict` would raise a false alarm against the very property
being measured. Folding it into `same_guid_match` would hide a real one.

`one_device_only` is expected and benign in bulk: the two Macs will not hold
identical history. It is interesting only where the fixture matrix says the
message should be present on both.

The report gives counts per class plus a bounded sample per class, identified
by local row ID only, so each operator can look the record up on their own
machine without either artifact carrying content.

## Fixture matrix

Both Macs must exercise all of this before a run is considered complete. Each
row states what result would be reassuring and what an unexpected result means.

**Services.** iMessage; SMS; MMS carrying an attachment; RCS where the OS and
carrier support it. RCS availability is recorded explicitly in the report — an
untested service must never be reported as passing.

**History age.** Messages predating the second Mac's setup, backfilled by
Messages in iCloud; and messages sent after both Macs were online. Backfilled
history is the most likely place for GUIDs to diverge, because the record is
reconstructed on the second machine rather than received live.

**Delivery path.** Sent from Mac A; sent from Mac B; sent from an iPhone on the
same account; received from another person. A message composed on one device
and mirrored to another is the case most likely to produce
`equivalent_different_guid`.

**Offline send.** Composed offline on one Mac and delivered once it reconnects.
A message that exists locally before the server sees it is a natural candidate
for GUID reassignment on delivery.

**Restore.** One Mac's Messages data restored from Time Machine or seeded by
Migration Assistant. This is the scenario that renumbers row IDs, and therefore
the one that most directly motivates the whole plan.

**Mutations.** An edited message; a retracted (unsent) message; a message
deleted on one Mac only; a message deleted on both. A mutation must change the
record's facts without changing its GUID; a GUID that changes on edit would
mean edits break citations.

**Attachments.** Zero, one, and several. Attachment count must agree across
machines for the same GUID.

**Reactions.** A tapback applied and a tapback removed. Reactions are separate
message rows pointing at a target GUID, so they test whether that pointer
survives independently.

**Grouping.** A one-to-one chat; a group chat; a chat renamed after messages
were sent. Chat GUIDs are collected too, since a stable chat identity is worth
as much as a stable message identity.

**Edge cases.** A message with empty text; a message with an attachment and no
text; a very long message. These test the digest's behaviour where text is
absent or unbounded.

## Decision criteria

GUIDs may become the identity authority only if, across the full matrix:

- `same_guid_conflict` is zero — one non-zero result is disqualifying, because
  it means a published reference could resolve to a different record;
- `missing_guid` is zero, or missing GUIDs form a bounded class that the
  canonical scheme grades `derived` or `local_only` and never claims is
  portable; and
- `equivalent_different_guid` is zero, or every instance is explained by a
  mechanism understood well enough to encode as a rule.

Anything else means GUIDs are not portable in general. That is a valid and
useful outcome: it would send step 3 toward a scheme that grades identity
honestly rather than one that overclaims.

Whatever the result, it must stay guarded by regression checks. Apple's
Messages database is private and can change between macOS releases, so a pass
today is evidence, not a guarantee.

## Running it

1. Confirm every unblocking condition above.
2. Exercise the fixture matrix on both Macs and let both finish syncing.
3. On each Mac, snapshot `chat.db` and run the collector against the snapshot.
4. Move both artifacts to one machine over a private channel.
5. Run the compare step.
6. Record the counts per class in this document, then delete the artifacts.

Counts derived from a real archive are private and stay out of the repository;
only the resulting decision and any synthetic regression fixtures are
committed.
