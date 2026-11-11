{{WITH}}
select count(*)
from messages m
{{FTS_JOIN}}
where m.source_rowid = (
  select newest.source_rowid
  from messages newest
  where newest.guid = m.guid
  order by newest.date desc, newest.source_rowid desc
  limit 1
)
{{FTS_FILTER}}
{{WHO_FILTER}}
{{TIME_FILTER}}
