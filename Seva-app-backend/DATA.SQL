
-- Tables
CREATE TABLE events (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    venue TEXT,
    tz TEXT NOT NULL DEFAULT 'UTC', -- Timezone of the event
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE committees (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, name) -- Committee names must be unique per event
);

CREATE TABLE faculty (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL, -- Email for login
    phone TEXT,
    department TEXT,
    password_hash TEXT, -- Hashed password for faculty login
    role user_role NOT NULL DEFAULT 'faculty', -- 'admin' or 'faculty'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE volunteers (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE, -- Email for login, must be unique if present
    phone TEXT,
    dept TEXT, -- Department (e.g., college department)
    college_id TEXT UNIQUE, -- Unique ID from college, if applicable
    password_hash TEXT, -- Hashed password for volunteer login (can be NULL initially if admin creates)
    role user_role NOT NULL DEFAULT 'volunteer',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE committee_faculty (
    committee_id BIGINT NOT NULL REFERENCES committees(id) ON DELETE CASCADE,
    faculty_id BIGINT NOT NULL REFERENCES faculty(id) ON DELETE CASCADE,
    role_note TEXT, -- e.g., "Lead Coordinator", "Logistics Head"
    PRIMARY KEY (committee_id, faculty_id)
);

CREATE TABLE volunteer_assignments (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    committee_id BIGINT NOT NULL REFERENCES committees(id) ON DELETE CASCADE,
    volunteer_id BIGINT NOT NULL REFERENCES volunteers(id) ON DELETE CASCADE,
    role assignment_role NOT NULL DEFAULT 'volunteer',
    status assignment_status NOT NULL DEFAULT 'assigned',
    reporting_time TIMESTAMPTZ, -- General reporting time, not necessarily shift start
    shift TEXT,          -- New: Name or description of the shift (e.g., "Morning Shift", "Setup Crew")
    start_time TIMESTAMPTZ, -- New: Specific start time for the shift
    end_time TIMESTAMPTZ,   -- New: Specific end time for the shift
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, committee_id, volunteer_id) -- A volunteer can only have one assignment per committee per event
);

CREATE TABLE attendance (
    id BIGSERIAL PRIMARY KEY,
    assignment_id BIGINT NOT NULL REFERENCES volunteer_assignments(id) ON DELETE CASCADE,
    check_in_time TIMESTAMPTZ NOT NULL,
    check_out_time TIMESTAMPTZ, -- Can be NULL if not checked out yet
    lat DOUBLE PRECISION,
    lng DOUBLE PRECISION
    -- Removed approved, approved_by, approved_at as per new requirements
);

CREATE TABLE announcements (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    committee_id BIGINT REFERENCES committees(id) ON DELETE SET NULL, -- NULL for event-wide announcements
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    priority announcement_priority NOT NULL DEFAULT 'normal',
    created_by BIGINT REFERENCES faculty(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ -- Can be NULL for no expiry
);

CREATE TABLE locations (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type location_type NOT NULL,
    description TEXT,
    lat DOUBLE PRECISION NOT NULL,
    lng DOUBLE PRECISION NOT NULL,
    UNIQUE (event_id, name) -- Location names unique per event
);

CREATE TABLE carbon_footprints (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    committee_id BIGINT REFERENCES committees(id) ON DELETE SET NULL,
    metric_date DATE NOT NULL,
    waste_bags INT NOT NULL DEFAULT 0,
    plastic_kg DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    volunteers_count INT NOT NULL DEFAULT 0,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (event_id, committee_id, metric_date) -- Only one entry per committee/event per day
);

CREATE TABLE auth_sessions (
    id BIGSERIAL PRIMARY KEY,
    faculty_id BIGINT NOT NULL REFERENCES faculty(id) ON DELETE CASCADE, -- Only faculty/admin get refresh tokens for now
    refresh_token_hash TEXT UNIQUE NOT NULL, -- Hashed refresh token
    user_agent TEXT,
    ip INET,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE api_keys (
    id BIGSERIAL PRIMARY KEY,
    label TEXT NOT NULL,
    role api_role NOT NULL DEFAULT 'viewer',
    key_hash TEXT UNIQUE NOT NULL, -- Hashed actual API key
    owner_faculty_id BIGINT REFERENCES faculty(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

CREATE TABLE audit_logs (
    id BIGSERIAL PRIMARY KEY,
    actor_type TEXT NOT NULL, -- e.g., 'faculty', 'volunteer', 'system', 'api_key'
    actor_id TEXT, -- Can be faculty.id, volunteer.id, api_key.id (as text)
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL, -- Optional, for event-scoped actions
    entity_table TEXT NOT NULL, -- e.g., 'volunteers', 'announcements'
    entity_id TEXT NOT NULL, -- ID of the entity that was acted upon (e.g., volunteer.id as text)
    action TEXT NOT NULL, -- e.g., 'create', 'update', 'delete', 'approve'
    diff JSONB, -- JSON representation of changes (e.g., {"old_value": ..., "new_value": ...})
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- NEW: Questions/FAQ Table
CREATE TABLE questions (
    id BIGSERIAL PRIMARY KEY,
    volunteer_id BIGINT REFERENCES volunteers(id) ON DELETE SET NULL, -- The volunteer who asked the question
    question_text TEXT NOT NULL,
    asked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    event_id BIGINT REFERENCES events(id) ON DELETE SET NULL, -- Optional: event context for the question
    committee_id BIGINT REFERENCES committees(id) ON DELETE SET NULL, -- Optional: committee context for the question
    answered_by BIGINT REFERENCES faculty(id) ON DELETE SET NULL, -- The faculty/admin who answered
    answer_text TEXT, -- NULL if not yet answered
    answered_at TIMESTAMPTZ -- NULL if not yet answered
);

-- Indexes for performance
CREATE INDEX idx_volunteers_email ON volunteers (lower(email));
CREATE INDEX idx_faculty_email ON faculty (lower(email));
CREATE INDEX idx_assignments_volunteer_id ON volunteer_assignments (volunteer_id);
CREATE INDEX idx_announcements_event_committee ON announcements (event_id, committee_id);
CREATE INDEX idx_attendance_assignment_id ON attendance (assignment_id);
CREATE INDEX idx_questions_volunteer_id ON questions (volunteer_id);
CREATE INDEX idx_questions_answered_by ON questions (answered_by);
CREATE INDEX idx_questions_status ON questions (answer_text IS NULL); -- For finding pending questions

-- Initial Admin Account (Optional, good for first run)
-- INSERT INTO faculty (name, email, password_hash, role) VALUES (
--     'Admin User',
--     'ADMIN_EMAIL_HERE', -- E.g., 'admin@example.com'
--     '$2a$10$yFwA6rKj2kK7N1fL7X9M.e.W.Q.Z.g.J.H.I.L.O.P.S.u.S.0.m.Y.X.C.c.z.A.q', -- 'password123' hashed
--     'admin'
-- ) ON CONFLICT (email) DO NOTHING;