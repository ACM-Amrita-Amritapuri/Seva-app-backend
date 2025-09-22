create extension if not exists pgcrypto;

do $$
begin
  if not exists (select 1 from pg_type where typname = 'announcement_priority') then
    create type announcement_priority as enum ('low','normal','high','urgent');
  end if;

  if not exists (select 1 from pg_type where typname = 'location_type') then
    create type location_type as enum ('stage','dining','helpdesk','parking','water','toilet','poi');
  end if;

  if not exists (select 1 from pg_type where typname = 'assignment_role') then
    create type assignment_role as enum ('volunteer','lead','support');
  end if;

  if not exists (select 1 from pg_type where typname = 'assignment_status') then
    create type assignment_status as enum ('assigned','standby','cancelled');
  end if;

  if not exists (select 1 from pg_type where typname = 'api_role') then
    create type api_role as enum ('admin','faculty','viewer');
  end if;
end$$;


create table if not exists events (
  id               bigserial primary key,
  name             text not null,
  venue            text,
  tz               text default 'Asia/Kolkata',
  starts_at        timestamptz,
  ends_at          timestamptz,
  created_at       timestamptz not null default now()
);

create table if not exists committees (
  id               bigserial primary key,
  event_id         bigint not null references events(id) on delete cascade,
  name             text not null,
  description      text default '',
  created_at       timestamptz not null default now(),
  unique (event_id, name)
);

create table if not exists faculty (
  id               bigserial primary key,
  name             text not null,
  email            text,
  phone            text,
  department       text
);

create table if not exists committee_faculty (
  committee_id     bigint not null references committees(id) on delete cascade,
  faculty_id       bigint not null references faculty(id) on delete cascade,
  role_note        text,
  primary key (committee_id, faculty_id)
);

create table if not exists volunteers (
  id               uuid primary key default gen_random_uuid(),
  name             text not null,
  email            text,
  phone            text,
  dept             text,
  college_id       text,
  created_at       timestamptz not null default now()
);

create table if not exists volunteer_assignments (
  id               bigserial primary key,
  event_id         bigint not null references events(id) on delete cascade,
  committee_id     bigint not null references committees(id) on delete cascade,
  volunteer_id     uuid   not null references volunteers(id) on delete cascade,
  role             assignment_role not null default 'volunteer',
  status           assignment_status not null default 'assigned',
  reporting_time   timestamptz,
  notes            text,
  created_at       timestamptz not null default now(),
  unique (event_id, committee_id, volunteer_id)
);

create index if not exists idx_va_committee on volunteer_assignments(committee_id);
create index if not exists idx_va_event on volunteer_assignments(event_id);

create table if not exists attendance (
  id               bigserial primary key,
  assignment_id    bigint not null references volunteer_assignments(id) on delete cascade,
  check_in_time    timestamptz not null default now(),
  check_out_time   timestamptz,
  lat              double precision,
  lng              double precision,
  approved         boolean not null default false,
  approved_by      bigint references faculty(id),
  approved_at      timestamptz,
  constraint chk_att_lat check (lat is null or (lat between -90 and 90)),
  constraint chk_att_lng check (lng is null or (lng between -180 and 180))
);

create index if not exists idx_attendance_approved on attendance(approved, check_in_time desc);
create index if not exists idx_attendance_assignment on attendance(assignment_id);

create table if not exists announcements (
  id               bigserial primary key,
  event_id         bigint not null references events(id) on delete cascade,
  committee_id     bigint references committees(id) on delete set null,
  title            text not null,
  body             text not null,
  priority         announcement_priority not null default 'normal',
  created_by       bigint references faculty(id),
  created_at       timestamptz not null default now(),
  expires_at       timestamptz
);

create index if not exists idx_ann_ev_committee on announcements(event_id, committee_id, created_at desc);

create table if not exists locations (
  id               bigserial primary key,
  event_id         bigint not null references events(id) on delete cascade,
  name             text not null,
  type             location_type not null default 'poi',
  description      text default '',
  lat              double precision not null,
  lng              double precision not null,
  constraint chk_loc_lat check (lat between -90 and 90),
  constraint chk_loc_lng check (lng between -180 and 180)
);

create index if not exists idx_locations_event on locations(event_id);

create table if not exists carbon_footprint (
  id               bigserial primary key,
  event_id         bigint not null references events(id) on delete cascade,
  committee_id     bigint references committees(id) on delete set null,
  metric_date      date not null default current_date,
  waste_bags       int  not null default 0,
  plastic_kg       numeric(10,2) not null default 0,
  volunteers_count int  not null default 0,
  notes            text,
  created_at       timestamptz not null default now(),
  unique (event_id, metric_date, coalesce(committee_id, 0))
);



create table if not exists api_keys (
  id               bigserial primary key,
  label            text not null,
  role             api_role not null default 'faculty',
  key_hash         bytea not null,              -- store hashed key (e.g., digest('key','sha256'))
  owner_faculty_id bigint references faculty(id),
  created_at       timestamptz not null default now(),
  revoked_at       timestamptz
);

create table if not exists audit_log (
  id               bigserial primary key,
  actor_type       text not null,               -- 'api_key','faculty','system'
  actor_id         text,
  event_id         bigint,
  entity_table     text not null,
  entity_id        text not null,
  action           text not null,               -- 'create','update','approve','delete'
  diff             jsonb,
  created_at       timestamptz not null default now()
);

create index if not exists idx_audit_event_time on audit_log(event_id, created_at desc);

----------------------------------------------------------------------
-- Convenience seeds (idempotent)
-- Adjust timestamps/coords as needed. Safe to keep for first boot.
----------------------------------------------------------------------

-- Seed event
insert into events (name, venue, tz, starts_at, ends_at)
select 'Amma Birthday 2025', 'Amritapuri', 'Asia/Kolkata',
       timestamptz '2025-09-26 07:00+05:30', timestamptz '2025-09-27 23:59+05:30'
where not exists (select 1 from events where name = 'Amma Birthday 2025');

-- Capture the event id into a psql variable for subsequent inserts (works in psql).
-- If your migration tool doesn't support variables, replace with the concrete id after first run.
-- \set ev_id (select id from events where name = 'Amma Birthday 2025' limit 1)

-- Committees (guarded)
insert into committees (event_id, name, description)
select e.id, x.name, ''
from events e
join (values
  ('May I Help You'),
  ('Volunteer Care Committee'),
  ('Plate Washing'),
  ('Venue Maintenance (Amala Bharatham)'),
  ('Special Invitees'),
  ('Press & Media'),
  ('Cultural')
) as x(name) on true
where e.name = 'Amma Birthday 2025'
  and not exists (
    select 1 from committees c
    where c.event_id = e.id and c.name = x.name
  );

-- Locations (guarded)
insert into locations (event_id, name, type, lat, lng, description)
select e.id, x.name, x.type::location_type, x.lat, x.lng, coalesce(x.desc,'')
from events e
join (values
  ('Main Stage','stage',  9.0950, 76.4850, 'Central stage'),
  ('Dining Hall A','dining', 9.0943, 76.4861, 'Primary dining'),
  ('Medical Desk','helpdesk', 9.0949, 76.4842, 'First aid')
) as x(name, type, lat, lng, desc) on true
where e.name = 'Amma Birthday 2025'
  and not exists (
    select 1 from locations l
    where l.event_id = e.id and l.name = x.name
  );

insert into faculty (name, email, phone, department)
select 'Event Approver', 'approver@example.org', null, 'Coordination'
where not exists (select 1 from faculty where email = 'approver@example.org');

-- users = faculty (reuse your table)
alter table faculty
  add column if not exists email text unique,
  add column if not exists password_hash text,      -- bcrypt/argon2id
  add column if not exists role api_role default 'faculty';  -- ('admin','faculty','viewer')

-- refresh tokens (rotating)
create table if not exists auth_sessions (
  id bigserial primary key,
  faculty_id bigint not null references faculty(id) on delete cascade,
  refresh_token_hash text not null,   -- store hash, not raw token
  user_agent text,
  ip inet,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,    -- e.g., now() + interval '30 days'
  revoked_at timestamptz
);

create index if not exists idx_auth_sessions_user on auth_sessions(faculty_id, expires_at);
