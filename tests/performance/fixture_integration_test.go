//go:build integration && performance

package performance

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/robinjoseph08/memento/internal/testdb"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

const fixtureNow = "2026-07-30T12:00:00Z"

type scaleFixture struct {
	db               *bun.DB
	curatorID        uuid.UUID
	curatorSession   uuid.UUID
	publicationEvent uuid.UUID
	proposalMoment   uuid.UUID
	sourceAlbum      uuid.UUID
	shape            FixtureShape
}

func newScaleFixture(t *testing.T) scaleFixture {
	t.Helper()
	base := testdb.Open(t)
	db := testdb.Clone(t, base, pgdriver.WithReadTimeout(20*time.Minute), pgdriver.WithWriteTimeout(20*time.Minute))
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(16)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	require.NoError(t, migrations.Apply(ctx, db))
	fixture := scaleFixture{
		db:               db,
		curatorID:        uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		curatorSession:   uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		publicationEvent: deterministicUUID("event", 1),
		proposalMoment:   deterministicUUID("moment", 21),
		sourceAlbum:      uuid.MustParse("00000000-0000-4000-8000-000000000003"),
	}
	require.NoError(t, fixture.seed(ctx))
	_, err := db.ExecContext(ctx, `ANALYZE`)
	require.NoError(t, err)
	fixture.shape = fixture.readAndValidateShape(t, ctx)
	return fixture
}

func deterministicUUID(kind string, index int) uuid.UUID {
	digest := md5.Sum([]byte(fmt.Sprintf("%s-%d", kind, index)))
	raw := hex.EncodeToString(digest[:])
	return uuid.MustParse(raw[:8] + "-" + raw[8:12] + "-4" + raw[13:16] + "-8" + raw[17:20] + "-" + raw[20:])
}

func (fixture scaleFixture) seed(ctx context.Context) error {
	return fixture.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		statements := []string{
			`UPDATE system_settings SET setup_complete = true WHERE id = 1`,
			`INSERT INTO people (id, display_name, sort_name) VALUES
			 ('00000000-0000-4000-8000-000000000001', 'Performance Curator', 'performance curator')`,
			`INSERT INTO person_roles (person_id, role) VALUES
			 ('00000000-0000-4000-8000-000000000001', 'curator'),
			 ('00000000-0000-4000-8000-000000000001', 'recipient')`,
			`INSERT INTO recipient_access_generations
			 (id, person_id, generation, state, onboarding_completed_at, created_at, updated_at)
			 VALUES ('00000000-0000-4000-8000-000000000004', '00000000-0000-4000-8000-000000000001', 1, 'completed', '` + fixtureNow + `', '` + fixtureNow + `', '` + fixtureNow + `')`,
			`INSERT INTO sessions
			 (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
			 SELECT '00000000-0000-4000-8000-000000000002', decode(repeat('42', 32), 'hex'),
			        '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000004', security_epoch,
			        'trusted', '` + fixtureNow + `'::timestamptz + interval '1 year' FROM system_settings WHERE id = 1`,
			`WITH generated AS (SELECT value,
			 (substr(md5('person-' || value),1,8)||'-'||substr(md5('person-' || value),9,4)||'-4'||substr(md5('person-' || value),14,3)||'-8'||substr(md5('person-' || value),18,3)||'-'||substr(md5('person-' || value),21,12))::uuid AS person_id,
			 (substr(md5('access-' || value),1,8)||'-'||substr(md5('access-' || value),9,4)||'-4'||substr(md5('access-' || value),14,3)||'-8'||substr(md5('access-' || value),18,3)||'-'||substr(md5('access-' || value),21,12))::uuid AS access_id
			 FROM generate_series(1, 50) value)
			 INSERT INTO people (id, display_name, sort_name)
			 SELECT person_id, 'Recipient ' || lpad(value::text, 2, '0'), 'recipient ' || lpad(value::text, 2, '0') FROM generated`,
			`WITH generated AS (SELECT value,
			 (substr(md5('person-' || value),1,8)||'-'||substr(md5('person-' || value),9,4)||'-4'||substr(md5('person-' || value),14,3)||'-8'||substr(md5('person-' || value),18,3)||'-'||substr(md5('person-' || value),21,12))::uuid AS person_id,
			 (substr(md5('access-' || value),1,8)||'-'||substr(md5('access-' || value),9,4)||'-4'||substr(md5('access-' || value),14,3)||'-8'||substr(md5('access-' || value),18,3)||'-'||substr(md5('access-' || value),21,12))::uuid AS access_id
			 FROM generate_series(1, 50) value)
			 INSERT INTO person_roles (person_id, role) SELECT person_id, 'recipient' FROM generated`,
			`WITH generated AS (SELECT value,
			 (substr(md5('person-' || value),1,8)||'-'||substr(md5('person-' || value),9,4)||'-4'||substr(md5('person-' || value),14,3)||'-8'||substr(md5('person-' || value),18,3)||'-'||substr(md5('person-' || value),21,12))::uuid AS person_id,
			 (substr(md5('access-' || value),1,8)||'-'||substr(md5('access-' || value),9,4)||'-4'||substr(md5('access-' || value),14,3)||'-8'||substr(md5('access-' || value),18,3)||'-'||substr(md5('access-' || value),21,12))::uuid AS access_id
			 FROM generate_series(1, 50) value)
			 INSERT INTO recipient_access_generations (id, person_id, generation, state, onboarding_completed_at, created_at, updated_at)
			 SELECT access_id, person_id, 1, 'completed', '` + fixtureNow + `', '` + fixtureNow + `', '` + fixtureNow + `' FROM generated`,
			`WITH generated AS (SELECT value,
			 (substr(md5('access-' || value),1,8)||'-'||substr(md5('access-' || value),9,4)||'-4'||substr(md5('access-' || value),14,3)||'-8'||substr(md5('access-' || value),18,3)||'-'||substr(md5('access-' || value),21,12))::uuid AS access_id
			 FROM generate_series(1, 50) value)
			 INSERT INTO recipient_emails (id, recipient_access_generation_id, email, normalized_email)
			 SELECT md5('email-' || value)::uuid, access_id, 'recipient-' || value || '@example.test', 'recipient-' || value || '@example.test' FROM generated`,
			`WITH generated AS (SELECT value,
			 (substr(md5('access-' || value),1,8)||'-'||substr(md5('access-' || value),9,4)||'-4'||substr(md5('access-' || value),14,3)||'-8'||substr(md5('access-' || value),18,3)||'-'||substr(md5('access-' || value),21,12))::uuid AS access_id
			 FROM generate_series(1, 50) value)
			 INSERT INTO notification_preferences (recipient_access_generation_id, email_preference, updated_at)
			 SELECT access_id, CASE WHEN value % 3 = 0 THEN 'weekly' ELSE 'immediate' END, '` + fixtureNow + `' FROM generated`,
			`WITH generated AS (SELECT value,
			 (substr(md5('person-' || value),1,8)||'-'||substr(md5('person-' || value),9,4)||'-4'||substr(md5('person-' || value),14,3)||'-8'||substr(md5('person-' || value),18,3)||'-'||substr(md5('person-' || value),21,12))::uuid AS person_id,
			 (substr(md5('access-' || value),1,8)||'-'||substr(md5('access-' || value),9,4)||'-4'||substr(md5('access-' || value),14,3)||'-8'||substr(md5('access-' || value),18,3)||'-'||substr(md5('access-' || value),21,12))::uuid AS access_id,
			 (substr(md5('session-' || value),1,8)||'-'||substr(md5('session-' || value),9,4)||'-4'||substr(md5('session-' || value),14,3)||'-8'||substr(md5('session-' || value),18,3)||'-'||substr(md5('session-' || value),21,12))::uuid AS session_id
			 FROM generate_series(1, 50) value)
			 INSERT INTO sessions (id, credential_hash, person_id, recipient_access_generation_id, security_epoch, session_type, idle_expires_at)
			 SELECT session_id, decode(md5('credential-a-' || value) || md5('credential-b-' || value), 'hex'), person_id, access_id,
			        settings.security_epoch, 'trusted', '` + fixtureNow + `'::timestamptz + interval '1 year'
			 FROM generated CROSS JOIN system_settings settings WHERE settings.id = 1`,
			`INSERT INTO source_albums (id, immich_album_id, name, asset_count, source_created_at, source_updated_at,
			 first_seen_at, last_seen_at, source_fingerprint, next_reconciliation_at, disposition)
			 VALUES ('00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000005', 'Target-scale source', 100000,
			 '` + fixtureNow + `', '` + fixtureNow + `', '` + fixtureNow + `', '` + fixtureNow + `', decode(repeat('11',32),'hex'), '` + fixtureNow + `', 'drafted')`,
			`WITH generated AS (SELECT value,
			 (substr(md5('media-' || value),1,8)||'-'||substr(md5('media-' || value),9,4)||'-4'||substr(md5('media-' || value),14,3)||'-8'||substr(md5('media-' || value),18,3)||'-'||substr(md5('media-' || value),21,12))::uuid AS media_id,
			 (substr(md5('asset-' || value),1,8)||'-'||substr(md5('asset-' || value),9,4)||'-4'||substr(md5('asset-' || value),14,3)||'-8'||substr(md5('asset-' || value),18,3)||'-'||substr(md5('asset-' || value),21,12))::uuid AS asset_id
			 FROM generate_series(1, 100000) value)
			 INSERT INTO media_items (id, immich_asset_id, media_type, width, height, local_date_time, first_seen_at, last_seen_at)
			 SELECT media_id, asset_id, CASE WHEN value % 20 = 0 THEN 'video' ELSE 'image' END, 1600, 1200,
			        to_char('2020-01-01'::date + (value % 2000), 'YYYY-MM-DD') || 'T12:00:00Z', '` + fixtureNow + `', '` + fixtureNow + `' FROM generated`,
			`INSERT INTO media_backings (id, media_item_id, immich_asset_id, filename, original_path, state, active, linked_at, confirmed_at)
			 SELECT md5('backing-' || id)::uuid, id, immich_asset_id, 'media-' || row_number() OVER () || '.jpg', '/fixture/media-' || row_number() OVER () || '.jpg',
			        'confirmed', true, '` + fixtureNow + `', '` + fixtureNow + `' FROM media_items`,
			`INSERT INTO source_album_memberships (source_album_id, immich_asset_id, media_item_id, first_seen_at, last_seen_at, source_fingerprint)
			 SELECT '00000000-0000-4000-8000-000000000003', immich_asset_id, id, '` + fixtureNow + `', '` + fixtureNow + `', decode(repeat('22',32),'hex') FROM media_items`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("seed target-scale identity or Media fixture: %w", err)
			}
		}
		return fixture.seedEvents(ctx, tx)
	})
}

func (fixture scaleFixture) seedEvents(ctx context.Context, tx bun.Tx) error {
	statements := []string{
		`WITH generated AS (SELECT value,
		 (substr(md5('event-' || value),1,8)||'-'||substr(md5('event-' || value),9,4)||'-4'||substr(md5('event-' || value),14,3)||'-8'||substr(md5('event-' || value),18,3)||'-'||substr(md5('event-' || value),21,12))::uuid event_id
		 FROM generate_series(1,21) value)
		 INSERT INTO events (id, lifecycle, title, description, grouping_timezone, version, final_review_complete, created_at, updated_at)
		 SELECT event_id, 'published', 'Family Event ' || lpad(value::text,2,'0') || CASE WHEN value=1 THEN ' reunion mountains' ELSE '' END,
		        'Target-scale fixture', 'UTC', 7, true, '` + fixtureNow + `', '` + fixtureNow + `' FROM generated`,
		`WITH generated AS (SELECT value,
		 (substr(md5('event-' || value),1,8)||'-'||substr(md5('event-' || value),9,4)||'-4'||substr(md5('event-' || value),14,3)||'-8'||substr(md5('event-' || value),18,3)||'-'||substr(md5('event-' || value),21,12))::uuid event_id,
		 (substr(md5('moment-' || value),1,8)||'-'||substr(md5('moment-' || value),9,4)||'-4'||substr(md5('moment-' || value),14,3)||'-8'||substr(md5('moment-' || value),18,3)||'-'||substr(md5('moment-' || value),21,12))::uuid moment_id
		 FROM generate_series(1,21) value)
		 INSERT INTO draft_moments (id,event_id,position,proposed_day,grouping_timezone,title,cover_media_item_id,attendance_complete,audience_complete)
		 SELECT moment_id,event_id,0,'2026-07-01'::date + value,'UTC','Moment '||value,
		 (substr(md5('media-' || CASE WHEN value <= 20 THEN (value-1)*5000+1 ELSE 1 END),1,8)||'-'||substr(md5('media-' || CASE WHEN value <= 20 THEN (value-1)*5000+1 ELSE 1 END),9,4)||'-4'||substr(md5('media-' || CASE WHEN value <= 20 THEN (value-1)*5000+1 ELSE 1 END),14,3)||'-8'||substr(md5('media-' || CASE WHEN value <= 20 THEN (value-1)*5000+1 ELSE 1 END),18,3)||'-'||substr(md5('media-' || CASE WHEN value <= 20 THEN (value-1)*5000+1 ELSE 1 END),21,12))::uuid,
		 true,true FROM generated`,
		`WITH pairs AS (SELECT event_no, position,
		 CASE WHEN event_no <= 20 THEN (event_no-1)*5000+position+1 ELSE position+1 END media_no
		 FROM generate_series(1,21) event_no CROSS JOIN generate_series(0,4999) position
		 WHERE event_no <= 20 OR position < 500)
		 INSERT INTO draft_media_placements (event_id,media_item_id,draft_moment_id,position,created_at)
		 SELECT
		 (substr(md5('event-' || event_no),1,8)||'-'||substr(md5('event-' || event_no),9,4)||'-4'||substr(md5('event-' || event_no),14,3)||'-8'||substr(md5('event-' || event_no),18,3)||'-'||substr(md5('event-' || event_no),21,12))::uuid,
		 (substr(md5('media-' || media_no),1,8)||'-'||substr(md5('media-' || media_no),9,4)||'-4'||substr(md5('media-' || media_no),14,3)||'-8'||substr(md5('media-' || media_no),18,3)||'-'||substr(md5('media-' || media_no),21,12))::uuid,
		 (substr(md5('moment-' || event_no),1,8)||'-'||substr(md5('moment-' || event_no),9,4)||'-4'||substr(md5('moment-' || event_no),14,3)||'-8'||substr(md5('moment-' || event_no),18,3)||'-'||substr(md5('moment-' || event_no),21,12))::uuid,
		 position,'` + fixtureNow + `' FROM pairs`,
		`WITH generated AS (SELECT value,
		 (substr(md5('moment-' || value),1,8)||'-'||substr(md5('moment-' || value),9,4)||'-4'||substr(md5('moment-' || value),14,3)||'-8'||substr(md5('moment-' || value),18,3)||'-'||substr(md5('moment-' || value),21,12))::uuid moment_id,
		 (substr(md5('snapshot-' || value),1,8)||'-'||substr(md5('snapshot-' || value),9,4)||'-4'||substr(md5('snapshot-' || value),14,3)||'-8'||substr(md5('snapshot-' || value),18,3)||'-'||substr(md5('snapshot-' || value),21,12))::uuid snapshot_id
		 FROM generate_series(1,21) value)
		 INSERT INTO audience_snapshots (id,target_kind,target_id,approved_by_person_id,approved_at,label)
		 SELECT snapshot_id,'moment',moment_id,'00000000-0000-4000-8000-000000000001','` + fixtureNow + `','Shared' FROM generated`,
		`WITH generated AS (SELECT value,
		 (substr(md5('moment-' || value),1,8)||'-'||substr(md5('moment-' || value),9,4)||'-4'||substr(md5('moment-' || value),14,3)||'-8'||substr(md5('moment-' || value),18,3)||'-'||substr(md5('moment-' || value),21,12))::uuid moment_id,
		 (substr(md5('snapshot-' || value),1,8)||'-'||substr(md5('snapshot-' || value),9,4)||'-4'||substr(md5('snapshot-' || value),14,3)||'-8'||substr(md5('snapshot-' || value),18,3)||'-'||substr(md5('snapshot-' || value),21,12))::uuid snapshot_id
		 FROM generate_series(1,21) value)
		 INSERT INTO current_audience_snapshots (target_kind,target_id,snapshot_id) SELECT 'moment',moment_id,snapshot_id FROM generated`,
		`WITH candidates AS (SELECT event_no, recipient_no FROM generate_series(1,21) event_no CROSS JOIN generate_series(1,50) recipient_no
		 WHERE (event_no=1 AND recipient_no<=2) OR (event_no>1 AND ((recipient_no-1-(event_no-1)*2+5000)%50)<2))
		 INSERT INTO audience_snapshot_entries (snapshot_id,recipient_person_id,recipient_access_generation_id)
		 SELECT
		 (substr(md5('snapshot-' || event_no),1,8)||'-'||substr(md5('snapshot-' || event_no),9,4)||'-4'||substr(md5('snapshot-' || event_no),14,3)||'-8'||substr(md5('snapshot-' || event_no),18,3)||'-'||substr(md5('snapshot-' || event_no),21,12))::uuid,
		 (substr(md5('person-' || recipient_no),1,8)||'-'||substr(md5('person-' || recipient_no),9,4)||'-4'||substr(md5('person-' || recipient_no),14,3)||'-8'||substr(md5('person-' || recipient_no),18,3)||'-'||substr(md5('person-' || recipient_no),21,12))::uuid,
		 (substr(md5('access-' || recipient_no),1,8)||'-'||substr(md5('access-' || recipient_no),9,4)||'-4'||substr(md5('access-' || recipient_no),14,3)||'-8'||substr(md5('access-' || recipient_no),18,3)||'-'||substr(md5('access-' || recipient_no),21,12))::uuid FROM candidates`,
		`WITH generated AS (SELECT value,
		 (substr(md5('moment-1-extra-' || value),1,8)||'-'||substr(md5('moment-1-extra-' || value),9,4)||'-4'||substr(md5('moment-1-extra-' || value),14,3)||'-8'||substr(md5('moment-1-extra-' || value),18,3)||'-'||substr(md5('moment-1-extra-' || value),21,12))::uuid moment_id
		 FROM generate_series(2,50) value)
		 INSERT INTO draft_moments (id,event_id,position,proposed_day,grouping_timezone,title,cover_media_item_id,attendance_complete,audience_complete)
		 SELECT moment_id,'` + deterministicUUID("event", 1).String() + `',value-1,'2026-07-02'::date+(value-1),'UTC','Moment 1-'||value,
		 (SELECT media_item_id FROM draft_media_placements WHERE event_id='` + deterministicUUID("event", 1).String() + `' ORDER BY position OFFSET (value-1)*100 LIMIT 1),true,true FROM generated`,
		`WITH generated AS (SELECT value,
		 (substr(md5('moment-1-extra-' || value),1,8)||'-'||substr(md5('moment-1-extra-' || value),9,4)||'-4'||substr(md5('moment-1-extra-' || value),14,3)||'-8'||substr(md5('moment-1-extra-' || value),18,3)||'-'||substr(md5('moment-1-extra-' || value),21,12))::uuid moment_id,
		 (substr(md5('snapshot-1-extra-' || value),1,8)||'-'||substr(md5('snapshot-1-extra-' || value),9,4)||'-4'||substr(md5('snapshot-1-extra-' || value),14,3)||'-8'||substr(md5('snapshot-1-extra-' || value),18,3)||'-'||substr(md5('snapshot-1-extra-' || value),21,12))::uuid snapshot_id
		 FROM generate_series(2,50) value), inserted AS (
		 INSERT INTO audience_snapshots (id,target_kind,target_id,approved_by_person_id,approved_at,label)
		 SELECT snapshot_id,'moment',moment_id,'00000000-0000-4000-8000-000000000001','` + fixtureNow + `','Shared' FROM generated RETURNING id,target_id)
		 INSERT INTO current_audience_snapshots (target_kind,target_id,snapshot_id) SELECT 'moment',target_id,id FROM inserted`,
		`WITH generated AS (SELECT value,
		 (substr(md5('moment-1-extra-' || value),1,8)||'-'||substr(md5('moment-1-extra-' || value),9,4)||'-4'||substr(md5('moment-1-extra-' || value),14,3)||'-8'||substr(md5('moment-1-extra-' || value),18,3)||'-'||substr(md5('moment-1-extra-' || value),21,12))::uuid moment_id,
		 (substr(md5('snapshot-1-extra-' || value),1,8)||'-'||substr(md5('snapshot-1-extra-' || value),9,4)||'-4'||substr(md5('snapshot-1-extra-' || value),14,3)||'-8'||substr(md5('snapshot-1-extra-' || value),18,3)||'-'||substr(md5('snapshot-1-extra-' || value),21,12))::uuid snapshot_id
		 FROM generate_series(2,50) value)
		 UPDATE draft_media_placements placement SET draft_moment_id=generated.moment_id
		 FROM generated WHERE placement.event_id='` + deterministicUUID("event", 1).String() + `' AND placement.position BETWEEN (generated.value-1)*100 AND generated.value*100-1`,
		`WITH generated AS (SELECT moment_no,recipient_no,
		 (substr(md5('snapshot-1-extra-' || moment_no),1,8)||'-'||substr(md5('snapshot-1-extra-' || moment_no),9,4)||'-4'||substr(md5('snapshot-1-extra-' || moment_no),14,3)||'-8'||substr(md5('snapshot-1-extra-' || moment_no),18,3)||'-'||substr(md5('snapshot-1-extra-' || moment_no),21,12))::uuid snapshot_id
		 FROM generate_series(2,50) moment_no CROSS JOIN LATERAL (VALUES (((moment_no-1)%50)+1), ((moment_no%50)+1)) recipients(recipient_no))
		 INSERT INTO audience_snapshot_entries (snapshot_id,recipient_person_id,recipient_access_generation_id)
		 SELECT snapshot_id,
		 (substr(md5('person-' || recipient_no),1,8)||'-'||substr(md5('person-' || recipient_no),9,4)||'-4'||substr(md5('person-' || recipient_no),14,3)||'-8'||substr(md5('person-' || recipient_no),18,3)||'-'||substr(md5('person-' || recipient_no),21,12))::uuid,
		 (substr(md5('access-' || recipient_no),1,8)||'-'||substr(md5('access-' || recipient_no),9,4)||'-4'||substr(md5('access-' || recipient_no),14,3)||'-8'||substr(md5('access-' || recipient_no),18,3)||'-'||substr(md5('access-' || recipient_no),21,12))::uuid FROM generated`,
		`INSERT INTO attendance (moment_id,person_id,source,confirmed_by_person_id,confirmed_at)
		 SELECT '` + deterministicUUID("moment", 21).String() + `',person_id,'manual','00000000-0000-4000-8000-000000000001','` + fixtureNow + `'
		 FROM recipient_access_generations WHERE person_id<>'00000000-0000-4000-8000-000000000001'`,
		`WITH generated AS (SELECT value,
		 (substr(md5('event-' || value),1,8)||'-'||substr(md5('event-' || value),9,4)||'-4'||substr(md5('event-' || value),14,3)||'-8'||substr(md5('event-' || value),18,3)||'-'||substr(md5('event-' || value),21,12))::uuid event_id,
		 (substr(md5('publication-' || value),1,8)||'-'||substr(md5('publication-' || value),9,4)||'-4'||substr(md5('publication-' || value),14,3)||'-8'||substr(md5('publication-' || value),18,3)||'-'||substr(md5('publication-' || value),21,12))::uuid publication_id
		 FROM generate_series(1,21) value)
		 INSERT INTO publications (id,event_id,revision,editable_version,published_by_person_id,notify_recipients,committed_at,content_revision)
		 SELECT publication_id,event_id,1,7,'00000000-0000-4000-8000-000000000001',true,'` + fixtureNow + `',value FROM generated`,
		`UPDATE system_settings SET content_revision = 21 WHERE id = 1`,
		`UPDATE events event SET current_publication_id=publication.id FROM publications publication WHERE publication.event_id=event.id`,
		`INSERT INTO published_event_revisions (publication_id,event_id,title,description,grouping_timezone,created_at)
		 SELECT publication.id,event.id,event.title,event.description,event.grouping_timezone,'` + fixtureNow + `' FROM publications publication JOIN events event ON event.id=publication.event_id`,
		`INSERT INTO published_moments (id,publication_id,draft_moment_id,audience_snapshot_id,position,title,proposed_day,cover_media_item_id)
		 SELECT md5('published-moment-' || moment.id)::uuid,publication.id,moment.id,snapshot.snapshot_id,moment.position,moment.title,moment.proposed_day,moment.cover_media_item_id
		 FROM draft_moments moment JOIN publications publication ON publication.event_id=moment.event_id
		 JOIN current_audience_snapshots snapshot ON snapshot.target_kind='moment' AND snapshot.target_id=moment.id`,
		`INSERT INTO published_media_placements (published_moment_id,media_item_id,position,media_type,width,height,local_date_time)
		 SELECT published.id,placement.media_item_id,placement.position,media.media_type,media.width,media.height,media.local_date_time
		 FROM published_moments published JOIN draft_media_placements placement ON placement.draft_moment_id=published.draft_moment_id
		 JOIN media_items media ON media.id=placement.media_item_id`,
		`INSERT INTO audience_entries (published_moment_id,recipient_person_id,recipient_access_generation_id)
		 SELECT published.id,entry.recipient_person_id,entry.recipient_access_generation_id FROM published_moments published
		 JOIN audience_snapshot_entries entry ON entry.snapshot_id=published.audience_snapshot_id`,
		`INSERT INTO current_published_events (event_id,publication_id,title,description,grouping_timezone,attendance_projection_ready,committed_at)
		 SELECT event.id,publication.id,event.title,event.description,event.grouping_timezone,true,'` + fixtureNow + `' FROM events event JOIN publications publication ON publication.event_id=event.id`,
		`INSERT INTO current_published_placements (
			event_id,publication_id,published_moment_id,media_item_id,position,
			media_type,width,height,local_date_time,capture_date
		 )
		 SELECT publication.event_id,publication.id,placement.published_moment_id,
			placement.media_item_id,placement.position,placement.media_type,placement.width,
			placement.height,placement.local_date_time,memento_local_capture_date(placement.local_date_time)
		 FROM publications publication JOIN published_moments moment ON moment.publication_id=publication.id
		 JOIN published_media_placements placement ON placement.published_moment_id=moment.id`,
		`INSERT INTO current_audience_entitlements (event_id,publication_id,recipient_person_id,recipient_access_generation_id,media_item_id)
		 SELECT publication.event_id,publication.id,audience.recipient_person_id,audience.recipient_access_generation_id,placement.media_item_id
		 FROM publications publication JOIN published_moments moment ON moment.publication_id=publication.id
		 JOIN audience_entries audience ON audience.published_moment_id=moment.id
		 JOIN published_media_placements placement ON placement.published_moment_id=moment.id`,
		`INSERT INTO current_recipient_event_covers (event_id,recipient_access_generation_id,media_item_id)
		 SELECT entitlement.event_id,entitlement.recipient_access_generation_id,(array_agg(entitlement.media_item_id ORDER BY entitlement.media_item_id))[1]
		 FROM current_audience_entitlements entitlement GROUP BY entitlement.event_id,entitlement.recipient_access_generation_id`,
		`INSERT INTO published_search_documents (event_id,publication_id,media_item_id,search_text,capture_date,place_text)
		 SELECT placement.event_id,placement.publication_id,placement.media_item_id,
		        current.title || ' family reunion mountains',published.local_date_time::date,'mountains home'
		 FROM current_published_placements placement JOIN current_published_events current ON current.event_id=placement.event_id
		 JOIN published_media_placements published ON published.published_moment_id=placement.published_moment_id AND published.media_item_id=placement.media_item_id`,
		`INSERT INTO publication_activity_items (publication_id,recipient_access_generation_id,created_at)
		 SELECT DISTINCT publication_id,recipient_access_generation_id,'` + fixtureNow + `'::timestamptz FROM current_audience_entitlements`,
		`INSERT INTO publication_notification_media (publication_id,recipient_access_generation_id,media_item_id)
		 SELECT activity.publication_id,activity.recipient_access_generation_id,(array_agg(entitlement.media_item_id ORDER BY entitlement.media_item_id))[1]
		 FROM publication_activity_items activity JOIN current_audience_entitlements entitlement
		 ON entitlement.publication_id=activity.publication_id AND entitlement.recipient_access_generation_id=activity.recipient_access_generation_id
		 GROUP BY activity.publication_id,activity.recipient_access_generation_id`,
		`INSERT INTO comments (id,media_item_id,author_person_id,author_access_generation_id,idempotency_key,body,created_at,updated_at)
		 SELECT md5('comment-' || media.id)::uuid,media.id,access.person_id,access.id,md5('comment-key-' || media.id)::uuid,'Fixture Comment '||row_number() OVER (),'` + fixtureNow + `','` + fixtureNow + `'
		 FROM (SELECT id,row_number() OVER (ORDER BY id) n FROM media_items ORDER BY id LIMIT 1000) media
		 JOIN (SELECT id,person_id,row_number() OVER (ORDER BY id) n FROM recipient_access_generations WHERE person_id<>'00000000-0000-4000-8000-000000000001') access
		 ON access.n=((media.n-1)%50)+1`,
		`INSERT INTO favorites (recipient_person_id,media_item_id,is_current,created_at,updated_at)
		 SELECT access.person_id,media.id,true,'` + fixtureNow + `','` + fixtureNow + `'
		 FROM (SELECT id,row_number() OVER (ORDER BY id) n FROM media_items ORDER BY id LIMIT 5000) media
		 JOIN (SELECT person_id,row_number() OVER (ORDER BY person_id) n FROM recipient_access_generations WHERE person_id<>'00000000-0000-4000-8000-000000000001') access
		 ON access.n=((media.n-1)%50)+1`,
		`INSERT INTO notification_batches (public_id,recipient_access_generation_id,channel,window_started_at,closes_at,status)
		 SELECT md5('batch-' || id)::uuid,id,'email','` + fixtureNow + `'::timestamptz-interval '15 minutes','` + fixtureNow + `','pending'
		 FROM recipient_access_generations WHERE person_id<>'00000000-0000-4000-8000-000000000001'`,
		`INSERT INTO notification_batch_items (batch_id,recipient_access_generation_id,kind,publication_id,activity_created_at)
		 SELECT batch.id,batch.recipient_access_generation_id,'publication',
		        (SELECT activity.publication_id FROM publication_activity_items activity WHERE activity.recipient_access_generation_id=batch.recipient_access_generation_id ORDER BY activity.publication_id LIMIT 1),
		        '` + fixtureNow + `'::timestamptz FROM notification_batches batch`,
		`INSERT INTO engagement_events (recipient_person_id,recipient_access_generation_id,session_id,kind,origin_key,occurred_at)
		 SELECT access.person_id,access.id,session.id,'visit','fixture-engagement-'||row_number() OVER (),'` + fixtureNow + `'
		 FROM recipient_access_generations access JOIN sessions session ON session.recipient_access_generation_id=access.id
		 WHERE access.person_id<>'00000000-0000-4000-8000-000000000001'`,
	}
	for index, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("seed target-scale Event fixture statement %d: %w", index+1, err)
		}
	}
	return nil
}

func (fixture scaleFixture) readAndValidateShape(t *testing.T, ctx context.Context) FixtureShape {
	t.Helper()
	shape := FixtureShape{}
	queries := []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM media_items`, &shape.MediaItems},
		{`SELECT count(*) FROM recipient_access_generations WHERE person_id <> '00000000-0000-4000-8000-000000000001'`, &shape.Recipients},
		{`SELECT count(*) FROM events`, &shape.Events},
		{`SELECT max(count) FROM (SELECT count(*) count FROM draft_media_placements GROUP BY event_id) grouped`, &shape.LargestEventPlacements},
		{`SELECT count(*) FROM (SELECT media_item_id FROM draft_media_placements GROUP BY media_item_id HAVING count(*) > 1) reused`, &shape.ReusedMediaItems},
		{`SELECT count(*) FROM audience_entries`, &shape.AudienceEntries},
		{`SELECT count(*) FROM comments`, &shape.Comments},
		{`SELECT count(*) FROM favorites`, &shape.Favorites},
		{`SELECT count(*) FROM published_search_documents`, &shape.SearchDocuments},
		{`SELECT count(*) FROM notification_batch_items`, &shape.DeliveryActivity},
	}
	for _, query := range queries {
		require.NoError(t, fixture.db.NewRaw(query.query).Scan(ctx, query.dest))
	}
	require.NoError(t, fixture.db.NewRaw(`SELECT count(DISTINCT recipient_access_generation_id) FROM current_audience_entitlements WHERE event_id = ?`, fixture.publicationEvent).Scan(ctx, &shape.PublicationRecipients))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM draft_media_placements WHERE draft_moment_id=?`, fixture.proposalMoment).Scan(ctx, &shape.ProposalMomentItems))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM attendance WHERE moment_id=?`, fixture.proposalMoment).Scan(ctx, &shape.AttendanceRows))
	require.NoError(t, fixture.db.NewRaw(`SELECT count(*) FROM (
		SELECT entry.recipient_access_generation_id
		FROM draft_moments moment
		JOIN current_audience_snapshots snapshot ON snapshot.target_kind='moment' AND snapshot.target_id=moment.id
		JOIN audience_snapshot_entries entry ON entry.snapshot_id=snapshot.snapshot_id
		WHERE moment.event_id=? GROUP BY entry.recipient_access_generation_id HAVING count(*)>1
	) overlapping`, fixture.publicationEvent).Scan(ctx, &shape.OverlappingRecipients))
	var fixtureDataChecksum string
	require.NoError(t, fixture.db.NewRaw(`SELECT md5(
		(SELECT string_agg(id::text,',' ORDER BY id) FROM media_items) ||
		(SELECT string_agg(event_id::text||':'||media_item_id::text||':'||position,',' ORDER BY event_id,position) FROM draft_media_placements) ||
		(SELECT string_agg(snapshot_id::text||':'||recipient_access_generation_id::text,',' ORDER BY snapshot_id,recipient_access_generation_id) FROM audience_snapshot_entries) ||
		(SELECT string_agg(recipient_person_id::text||':'||media_item_id::text,',' ORDER BY recipient_person_id,media_item_id) FROM favorites) ||
		(SELECT string_agg(recipient_access_generation_id::text||':'||publication_id::text,',' ORDER BY recipient_access_generation_id,publication_id) FROM publication_activity_items) ||
		(SELECT string_agg(id::text||':'||media_item_id::text||':'||body,',' ORDER BY id) FROM comments) ||
		(SELECT string_agg(event_id::text||':'||media_item_id::text||':'||search_text,',' ORDER BY event_id,media_item_id) FROM published_search_documents) ||
		(SELECT string_agg(public_id::text||':'||recipient_access_generation_id::text,',' ORDER BY public_id) FROM notification_batches) ||
		(SELECT string_agg(origin_key,',' ORDER BY origin_key) FROM engagement_events)
	)`).Scan(ctx, &fixtureDataChecksum))
	encodedShape, err := json.Marshal(struct {
		Shape FixtureShape `json:"shape"`
		Data  string       `json:"data"`
	}{Shape: shape, Data: fixtureDataChecksum})
	require.NoError(t, err)
	shapeDigest := sha256.Sum256(encodedShape)
	shape.Checksum = hex.EncodeToString(shapeDigest[:])
	require.Equal(t, 100000, shape.MediaItems)
	require.Equal(t, 50, shape.Recipients)
	require.Equal(t, 5000, shape.LargestEventPlacements)
	require.Equal(t, 21, shape.Events)
	require.Positive(t, shape.ReusedMediaItems)
	require.Positive(t, shape.AudienceEntries)
	require.Equal(t, 50, shape.PublicationRecipients)
	require.Positive(t, shape.OverlappingRecipients)
	require.Equal(t, 500, shape.ProposalMomentItems)
	require.Equal(t, 50, shape.AttendanceRows)
	require.Positive(t, shape.Comments)
	require.Positive(t, shape.Favorites)
	require.Positive(t, shape.SearchDocuments)
	require.Positive(t, shape.DeliveryActivity)
	return shape
}
