with chat_summaries as (
  select
    c.source_rowid,
    c.chat_id,
    c.guid,
    coalesce(nullif(trim(c.display_name), ''), nullif(trim(c.room_name), ''), nullif(trim(c.chat_identifier), ''), c.guid) as title,
    case
      when count(distinct cp.handle_rowid) > 1 or nullif(trim(c.room_name), '') is not null then 'group'
      else 'direct'
    end as kind,
    coalesce(c.chat_identifier, '') as chat_identifier,
    coalesce(c.room_name, '') as room_name,
    coalesce(c.service_name, '') as service_name,
    count(distinct cp.handle_rowid) as participants,
    count(distinct cm.message_rowid) as messages,
    coalesce(max(m.date), 0) as latest_message,
    {{UNREAD_SELECT}} as unread,
    row_number() over (
      partition by c.chat_id
      order by coalesce(max(m.date), 0) desc, c.source_rowid desc
    ) as chat_copy
  from chats c
  left join chat_participants cp on cp.chat_rowid = c.source_rowid
  left join chat_messages cm on cm.chat_rowid = c.source_rowid
  left join messages m on m.source_rowid = cm.message_rowid
  {{WHERE}}
  group by c.source_rowid, c.guid, c.display_name, c.room_name, c.chat_identifier, c.service_name
)
select source_rowid, chat_id, guid, title, kind, chat_identifier, room_name, service_name,
  participants, messages, latest_message, unread
from chat_summaries
where chat_copy = 1 {{UNREAD_FILTER}}
order by latest_message desc, source_rowid desc
