CREATE TABLE IF NOT EXISTS tags (
    id BIGSERIAL PRIMARY KEY,
    name TEXT,
    color TEXT,
    wikidata_qid TEXT UNIQUE
);

CREATE TABLE IF NOT EXISTS events (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT,
    wiki_url TEXT,
    view_priority INTEGER DEFAULT 0,
    importance REAL DEFAULT 0,
    
    year_start INTEGER NOT NULL,
    month_start INTEGER DEFAULT 0,
    day_start INTEGER DEFAULT 0,
    
    year_end INTEGER,
    month_end INTEGER DEFAULT 0,
    day_end INTEGER DEFAULT 0,
    
    precision INTEGER NOT NULL DEFAULT 11,
    is_bce BOOLEAN DEFAULT FALSE,
    date_display TEXT,
    image_url TEXT,
    end_date_display TEXT,
    is_end_bce BOOLEAN DEFAULT FALSE,
    
    latitude REAL,
    longitude REAL
);

CREATE TABLE IF NOT EXISTS event_tags (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT REFERENCES events(id) ON DELETE CASCADE,
    tag_id BIGINT REFERENCES tags(id) ON DELETE CASCADE,
    wikidata_property TEXT
);

-- CREATE INDEX IF NOT EXISTS idx_events_timeline 
-- ON events (is_bce, year_start, month_start, day_start);

-- CREATE INDEX IF NOT EXISTS idx_events_ranking 
-- ON events (view_priority DESC, importance DESC);

-- CREATE INDEX IF NOT EXISTS idx_events_geo 
-- ON events (latitude, longitude) 
-- WHERE latitude IS NOT NULL AND longitude IS NOT NULL;

-- CREATE INDEX IF NOT EXISTS idx_event_tags_lookup 
-- ON event_tags (tag_id, event_id);

