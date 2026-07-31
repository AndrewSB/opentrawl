---
written_by: ai
---

# Stable reference identity plan

This is a temporary working plan, not a product contract. Delete it once the
delivery order below has landed and the durable promises live in
[the crawler control contract](contract.md), source documentation and tests.

OpenTrawl references are durable citations. A reference must continue to name
the same source record across repeated syncs, archive rebuilds, restores and
independent machines that acquire the same provider record. If OpenTrawl cannot
prove that continuity, it must report an identity conflict instead of silently
reassigning a reference.

## Current risks

The iMessage crawler currently constructs message and chat references from
machine-local SQLite row IDs. A full archive sync also replaces source-derived
tables and clears the short-reference index before the shared assignment logic
runs. This creates two distinct risks:

- independently synced machines may assign different canonical references to
  the same provider record; and
- a rebuilt short-reference corpus may assign a different alias to a
  previously indexed canonical reference.

SQLite integrity validation proves that a replica is structurally readable. It
does not prove identity continuity.

## Required contract

Every source record has:

- a source and record kind;
- a normalized provider identity key when the provider supplies one;
- an opaque canonical OpenTrawl reference derived from the provider identity;
- an identity grade of `provider_stable`, `derived` or `local_only`; and
- permanent legacy and short-reference aliases.

Canonical references are source-scoped, opaque and collision resistant. They
do not expose provider identifiers, account identifiers or record content.
Machine-local database keys remain internal join and ordering details.

Once published, a reference may resolve to its original record or to an
explicit tombstone. It must never resolve to a different record.

## Establish the iMessage identity authority

Apple Messages exposes both local SQLite row IDs and message GUIDs. Before
making GUIDs authoritative, validate their observed behavior across two Macs
independently synchronized from the same iCloud account.

The audit compares mechanical identity facts without printing or retaining
message content:

- normalized GUID;
- timestamp, direction and service;
- attachment count; and
- a bounded content digest.

It classifies same-GUID matches, same-GUID conflicts, equivalent records with
different GUIDs, missing GUIDs and records present on only one device. The
fixture matrix covers iMessage, SMS, MMS and RCS; old and new history; offline
sends; restores; edits, retractions and deletions; attachments; and reactions.

Apple's private Messages database is not a public API. Observed GUID stability
must therefore remain guarded by regression checks and explicit conflict
handling.

## Canonical reference scheme

When a normalized provider key is available, derive the canonical token from a
domain-separated digest:

```text
SHA-256(
  "opentrawl-ref-v1\0" +
  source + "\0" +
  record_kind + "\0" +
  normalized_provider_key
)
```

Encode at least 160 bits with a URL- and terminal-safe alphabet:

```text
imessage:message:<opaque-token>
imessage:chat:<opaque-token>
```

Equivalent provider identities produce the same reference regardless of local
row ID, insertion order, archive layout or machine. Source and record-kind
domain separation prevents cross-kind collisions.

A record without a trustworthy provider identity is marked `derived` or
`local_only`. OpenTrawl does not claim cross-machine equivalence for it.
Content fingerprints may surface reconciliation candidates, but must not
silently merge records.

## Immutable identity ledger

Store identity separately from replaceable source-derived tables:

```sql
create table record_identities (
  source text not null,
  record_kind text not null,
  provider_key text not null,
  canonical_ref text not null,
  identity_grade text not null,
  first_seen_at text not null,
  last_seen_at text not null,
  tombstoned_at text,
  primary key (source, record_kind, provider_key),
  unique (canonical_ref)
);

create table ref_aliases (
  alias text primary key,
  canonical_ref text not null,
  alias_kind text not null
);
```

Sync never deletes these tables. Records absent from a later complete snapshot
become tombstones; their identities and aliases remain reserved.

## Migration

For every existing iMessage record:

1. Compute its provider-key canonical reference.
2. Record the current row-ID reference as a permanent legacy alias.
3. Preserve every existing short reference.
4. Resolve old and new references to the new canonical identity.
5. Stop with an identity-conflict error on duplicate or inconsistent mappings.

After migration, output the new canonical reference. Existing citations remain
valid through aliases. A reused local row ID can never rebind a legacy
reference to another record.

## Short references

Immediately stop deleting the short-reference index during source replacement.
The existing assign-only mechanism then preserves previously issued aliases.

Canonical references are the portable citation contract. If short references
must also be identical across independently built machines, derive them from a
fixed, sufficiently long prefix of the canonical digest. Do not choose their
length from the local corpus. A collision is an explicit correctness failure,
not permission to move an existing alias.

## Replication continuity

Each archive records:

- source and identity-scheme version;
- crawler schema version;
- an irreversible logical-account fingerprint;
- an identity-map digest; and
- archive lineage.

Before replacing a replica, compare this manifest with the destination.
Replication rejects incompatible accounts, identity schemes, canonical
reference conflicts, legacy-alias reassignment and broken lineage. SQLite
`quick_check` remains a separate structural-integrity check.

## Acceptance tests

1. Repeated sync of one source database preserves canonical and short refs.
2. Equal GUIDs under different local row IDs produce equal canonical refs.
3. Different insertion orders produce equal canonical refs.
4. Additions and deletions do not change existing refs or aliases.
5. Full source-table replacement preserves the identity ledger and aliases.
6. A forced digest-prefix collision fails safely.
7. Alternating replication from compatible machines preserves references.
8. Legacy row-ID references resolve after migration.
9. A reused row ID cannot rebind an old reference.
10. A shared GUID with conflicting mechanical facts stops sync.
11. A missing GUID is explicitly graded and never falsely claimed portable.
12. Replication rejects incompatible identity lineage.

## Delivery order

1. Remove `short_refs` from iMessage snapshot deletion and add a two-sync
   regression test.
2. Build and run the two-machine GUID audit.
3. Ratify the cross-source identity contract and canonical token encoding.
4. Add the immutable identity ledger and conflict checks.
5. Migrate iMessage with permanent aliases for existing references.
6. Add replication lineage validation.
7. Convert full replacement to incremental upserts without changing identity.
8. Apply the same provider-identity review to every crawler.

The first step fixes a known contract violation. Later steps establish the
stronger promise: references remain attached to the same provider record over
months and across compatible machines, or OpenTrawl fails explicitly.
