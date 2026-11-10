-- Employees (design.md): one person's employment at one legal entity; the number is the legal entity's once.
CREATE TABLE employee (
    id               uuid PRIMARY KEY,
    legal_entity_id  uuid NOT NULL,
    employee_no      text NOT NULL CHECK (employee_no ~ '^[A-Z0-9][A-Z0-9_-]{0,31}$'),
    name             text NOT NULL CHECK (name <> ''),
    names            jsonb NOT NULL DEFAULT '{}'::jsonb,
    state            text NOT NULL CHECK (state IN ('pre_hire', 'active', 'terminated')),
    hire_date        date NOT NULL,
    termination_date date,
    end_reason       text CHECK (end_reason IN ('terminated', 'cancelled')),
    -- restricted personal data (security 09a): never in an event, read only by a grant that lists the field
    birth_date       date,
    national_id      text,
    search           text NOT NULL,
    search_version   integer NOT NULL,
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (legal_entity_id, employee_no),
    CHECK ((state = 'terminated') = (end_reason IS NOT NULL))
);
-- the effective-dates job: hires and terminations that come due
CREATE INDEX employee_due ON employee (state, hire_date, termination_date) WHERE state <> 'terminated';

-- Appointments: qualification instances, never reused (security 04). unit_path is the unit's org-tree path, copied
-- from the mdm/org projection for the department dimension; it never changes for an appointment's life except when
-- the unit itself moves (the projection rewrites it).
CREATE TABLE appointment (
    id              uuid PRIMARY KEY,
    employee_id     uuid NOT NULL REFERENCES employee (id),
    legal_entity_id uuid NOT NULL,
    unit_id         uuid NOT NULL,
    unit_path       text NOT NULL,
    position_id     uuid NOT NULL,
    start_date      date NOT NULL,
    end_date        date,
    state           text NOT NULL CHECK (state IN ('planned', 'active', 'ended')),
    end_reason      text CHECK (end_reason IN ('transfer', 'termination', 'position_retired', 'unit_retired', 'expired', 'ended')),
    note            text NOT NULL DEFAULT '',
    version         bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (end_date IS NULL OR end_date >= start_date - 1),
    CHECK ((state = 'ended') = (end_reason IS NOT NULL))
);
CREATE INDEX appointment_employee ON appointment (employee_id, start_date);
CREATE INDEX appointment_position ON appointment (position_id) WHERE state <> 'ended';
CREATE INDEX appointment_unit ON appointment (unit_id) WHERE state <> 'ended';

-- Account links: an account to at most one employee at a time; every confirmation used once (security 08).
CREATE TABLE account_link (
    id               uuid PRIMARY KEY,
    employee_id      uuid NOT NULL REFERENCES employee (id),
    legal_entity_id  uuid NOT NULL,
    account          text NOT NULL CHECK (account <> ''),
    state            text NOT NULL CHECK (state IN ('active', 'ended')),
    confirmation     text NOT NULL UNIQUE,
    end_confirmation text UNIQUE,
    linked_at        timestamptz NOT NULL DEFAULT now(),
    ended_at         timestamptz,
    version          bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((state = 'ended') = (ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX account_link_active ON account_link (account) WHERE state = 'active';
CREATE INDEX account_link_employee ON account_link (employee_id);

-- One queued alignment of the mdm/org projection at a time (internal/orgview: mdm/org's epoch changed).
CREATE TABLE org_realign (
    one boolean PRIMARY KEY DEFAULT true CHECK (one)
);
