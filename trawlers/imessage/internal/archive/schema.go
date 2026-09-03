package archive

const schema = `
create table if not exists handles (
  source_rowid integer primary key,
  handle text not null,
  service text not null,
  uncanonicalized_id text,
  display_name text
);

create table if not exists chats (
  source_rowid integer primary key,
  guid text not null,
  chat_identifier text,
  service_name text,
  display_name text,
  room_name text,
  is_archived integer not null default 0
);

create table if not exists chat_participants (
  chat_rowid integer not null,
  handle_rowid integer not null,
  primary key (chat_rowid, handle_rowid)
);

create table if not exists chat_messages (
  chat_rowid integer not null,
  message_rowid integer not null,
  primary key (chat_rowid, message_rowid)
);

create table if not exists messages (
  source_rowid integer primary key,
  guid text not null,
  handle_rowid integer not null default 0,
  date integer not null default 0,
  service text,
  account text,
  is_from_me integer not null default 0,
  text text,
  has_attachments integer not null default 0,
  is_read integer not null default 0,
  is_forward integer,
  item_type integer,
  group_action_type integer,
  message_action_type integer,
  associated_message_type integer
);

` + appleCashMessagesSchema + `

create virtual table if not exists messages_fts using fts5(source_rowid unindexed, text);

create table if not exists contact_mappings (
  kind text not null,
  normalized_handle text not null,
  contact_key text not null default '',
  display_name text not null,
  primary key (kind, normalized_handle)
);

create table if not exists owner_handles (
  kind text not null,
  normalized_handle text not null,
  primary key (kind, normalized_handle)
);

create index if not exists idx_chat_messages_chat on chat_messages(chat_rowid, message_rowid);
create index if not exists idx_chat_messages_message on chat_messages(message_rowid, chat_rowid);
create index if not exists idx_messages_date on messages(date, source_rowid);
`

const appleCashMessagesSchema = `create table if not exists apple_cash_messages (
  message_rowid integer primary key,
  version integer not null,
  identifier text not null,
  payment_kind integer not null,
  currency_code text not null,
  legacy_amount integer not null,
  sender_address text not null,
  recipient_address text not null,
  request_token text not null,
  payment_identifier text not null,
  transaction_identifier text not null,
  memo text not null,
  request_device_score_identifier text not null,
  payment_source integer not null,
  recurring_payment_identifier text not null,
  recurring_payment_emoji text not null,
  recurring_payment_color text not null,
  recurring_payment_start_date real not null,
  recurring_payment_frequency text not null,
  has_decimal_amount integer not null,
  decimal_version integer not null,
  decimal_exponent integer not null,
  decimal_length integer not null,
  decimal_is_negative integer not null,
  decimal_is_compact integer not null,
  decimal_reserved integer not null,
  decimal_mantissa blob not null,
  local_data blob not null,
  messages_context integer not null,
  payment_signature text not null,
  messages_group_identifier text not null,
  source_display_text text not null
);`
