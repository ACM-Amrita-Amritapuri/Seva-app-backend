-- Create ENUM types first
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'user_role') THEN
        CREATE TYPE user_role AS ENUM ('admin', 'faculty', 'volunteer');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'announcement_priority') THEN
        CREATE TYPE announcement_priority AS ENUM ('low', 'normal', 'high', 'urgent');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'location_type') THEN
        CREATE TYPE location_type AS ENUM ('stage', 'dining', 'helpdesk', 'parking', 'water', 'toilet', 'poi');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'assignment_role') THEN
        CREATE TYPE assignment_role AS ENUM ('volunteer', 'lead', 'support');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'assignment_status') THEN
        CREATE TYPE assignment_status AS ENUM ('assigned', 'standby', 'cancelled');
    END IF;
END $$;

-- Table: events
CREATE TABLE IF NOT EXISTS events (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    venue TEXT,
    tz TEXT NOT NULL DEFAULT 'UTC', -- Default to UTC if not specified
    starts_at TIMESTAMP WITH TIME ZONE,
    ends_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Table: faculty
CREATE TABLE IF NOT EXISTS faculty (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE, -- Email can be null but if present, must be unique
    phone TEXT,
    department TEXT,
    role user_role NOT NULL DEFAULT 'faculty',
    password_hash TEXT, -- Nullable if account is pre-created without password
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW() -- Added for consistency
);

-- Table: volunteers
CREATE TABLE IF NOT EXISTS volunteers (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE, -- Email can be null but if present, must be unique
    phone TEXT,
    dept TEXT,
    college_id TEXT UNIQUE, -- College ID can be null but if present, must be unique
    password_hash TEXT, -- Nullable if account is pre-created without password
    role user_role NOT NULL DEFAULT 'volunteer',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Table: committees
CREATE TABLE IF NOT EXISTS committees (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(event_id, name) -- Committee names must be unique within an event
);

-- Table: announcements
CREATE TABLE IF NOT EXISTS announcements (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    committee_id BIGINT REFERENCES committees(id) ON DELETE SET NULL, -- Committee is optional
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    priority announcement_priority NOT NULL DEFAULT 'normal',
    created_by BIGINT REFERENCES faculty(id) ON DELETE SET NULL, -- Creator faculty member
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE
);

-- Table: locations
CREATE TABLE IF NOT EXISTS locations (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type location_type NOT NULL,
    description TEXT,
    lat DOUBLE PRECISION NOT NULL,
    lng DOUBLE PRECISION NOT NULL,
    UNIQUE(event_id, name) -- Location names must be unique within an event
);

-- Table: volunteer_assignments
CREATE TABLE IF NOT EXISTS volunteer_assignments (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    committee_id BIGINT NOT NULL REFERENCES committees(id) ON DELETE CASCADE,
    volunteer_id BIGINT NOT NULL REFERENCES volunteers(id) ON DELETE CASCADE,
    role assignment_role NOT NULL DEFAULT 'volunteer',
    status assignment_status NOT NULL DEFAULT 'assigned',
    reporting_time TIMESTAMP WITH TIME ZONE,
    shift TEXT, -- e.g., "Morning Shift", "Setup Crew"
    start_time TIMESTAMP WITH TIME ZONE,
    end_time TIMESTAMP WITH TIME ZONE,
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(event_id, committee_id, volunteer_id) -- A volunteer can only have one assignment per committee per event
);

-- Table: attendance
CREATE TABLE IF NOT EXISTS attendance (
    id BIGSERIAL PRIMARY KEY,
    assignment_id BIGINT NOT NULL REFERENCES volunteer_assignments(id) ON DELETE CASCADE,
    check_in_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    check_out_time TIMESTAMP WITH TIME ZONE, -- Null if still checked in
    lat DOUBLE PRECISION,
    lng DOUBLE PRECISION
);

-- Add a partial unique index to prevent multiple active check-ins for the same assignment on the same day.
-- We cast to 'timestamp without time zone' in 'UTC' to make the expression IMMUTABLE before taking the date part.
CREATE UNIQUE INDEX ux_attendance_active_assignment_day
ON attendance (assignment_id, ((check_in_time AT TIME ZONE 'UTC')::date))
WHERE check_out_time IS NULL;


-- Table: carbon_footprint
CREATE TABLE IF NOT EXISTS carbon_footprint (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    committee_id BIGINT REFERENCES committees(id) ON DELETE SET NULL, -- Committee is optional
    metric_date DATE NOT NULL,
    waste_bags INT NOT NULL DEFAULT 0 CHECK (waste_bags >= 0),
    plastic_kg DOUBLE PRECISION NOT NULL DEFAULT 0.0 CHECK (plastic_kg >= 0),
    volunteers_count INT NOT NULL DEFAULT 0 CHECK (volunteers_count >= 0),
    notes TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(event_id, committee_id, metric_date)
);

-- Table: auth_sessions (for Faculty/Admin refresh tokens)
CREATE TABLE IF NOT EXISTS auth_sessions (
    id BIGSERIAL PRIMARY KEY,
    faculty_id BIGINT NOT NULL REFERENCES faculty(id) ON DELETE CASCADE,
    refresh_token_hash TEXT NOT NULL UNIQUE, -- Hashed refresh token
    user_agent TEXT,
    ip TEXT,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    revoked_at TIMESTAMP WITH TIME ZONE, -- Set when token is explicitly revoked or rotated
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Table: api_keys
CREATE TABLE IF NOT EXISTS api_keys (
    id BIGSERIAL PRIMARY KEY,
    label TEXT NOT NULL,
    role user_role NOT NULL,
    key_hash BYTEA NOT NULL UNIQUE, -- Hashed API key
    owner_faculty_id BIGINT REFERENCES faculty(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMP WITH TIME ZONE -- Set if key is revoked
);

-- Table: audit_logs
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_type TEXT NOT NULL, -- e.g., 'admin', 'faculty', 'volunteer', 'api_key'
    actor_id TEXT, -- ID of the actor (e.g., faculty_id, volunteer_id, api_key_id). Stored as TEXT due to potential varied source tables.
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL, -- Optional event context
    entity_table TEXT NOT NULL, -- Table where the change occurred (e.g., 'volunteers', 'announcements')
    entity_id TEXT NOT NULL, -- ID of the entity that was changed (e.g., '1', 'UUID-XYZ'). Stored as TEXT.
    action TEXT NOT NULL, -- e.g., 'create', 'update', 'delete', 'login', 'checkin'
    diff JSONB, -- JSONB to store a diff of the changes (e.g., old vs new values)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Table: questions (May I Help You)
CREATE TABLE IF NOT EXISTS questions (
    id BIGSERIAL PRIMARY KEY,
    volunteer_id BIGINT REFERENCES volunteers(id) ON DELETE SET NULL, -- Nullable if anonymous questions are allowed
    question_text TEXT NOT NULL,
    asked_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL, -- Optional event context for the question
    committee_id BIGINT REFERENCES committees(id) ON DELETE SET NULL, -- Optional committee context for the question
    answered_by BIGINT REFERENCES faculty(id) ON DELETE SET NULL, -- The faculty member who answered
    answer_text TEXT, -- Null if not yet answered
    answered_at TIMESTAMP WITH TIME ZONE -- Null if not yet answered
);
INSERT INTO events (name, venue, tz, starts_at, ends_at)
SELECT 'Amma Birthday 2025', 'Amritapuri', 'Asia/Kolkata',
       TIMESTAMPTZ '2025-09-26 07:00:00+05:30', TIMESTAMPTZ '2025-09-27 23:59:00+05:30'
WHERE NOT EXISTS (SELECT 1 FROM events WHERE name = 'Amma Birthday 2025');

INSERT INTO faculty (name, email, password_hash, role) VALUES (
    'Admin User',
    'admin@example.com', -- E.g., 'admin@example.com'
    '$2b$12$DgWqOsehDXrQx9wxMGP70u6P/TtXrO.YL1qCfU.dHSMuQqJcOyg86', 
    'admin'
) ON CONFLICT (email) DO NOTHING;
