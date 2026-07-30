package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	collection.MustRegister(
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`ALTER TABLE curator_activity_items ALTER COLUMN actor_person_id DROP NOT NULL`,
					`ALTER TABLE curator_activity_items ALTER COLUMN invitation_suggestion_id DROP NOT NULL`,
					`ALTER TABLE curator_activity_items DROP CONSTRAINT curator_activity_items_action_check`,
					`ALTER TABLE curator_activity_items ADD CONSTRAINT curator_activity_items_action_check CHECK (action <> '')`,
					`ALTER TABLE curator_activity_items
						ADD COLUMN source_kind text,
						ADD COLUMN source_id text,
						ADD COLUMN version text,
						ADD COLUMN category text,
						ADD COLUMN subject_person_id uuid REFERENCES people(id) ON DELETE RESTRICT,
						ADD COLUMN target_kind text,
						ADD COLUMN target_id text,
						ADD COLUMN target_label text,
						ADD COLUMN outcome text`,
					`UPDATE curator_activity_items AS activity SET
						source_kind = 'invitation_suggestion_activity', source_id = activity.id::text,
						version = 'suggestion activity ' || activity.id::text, category = 'invitation_suggestion',
						subject_person_id = suggestion.requester_person_id,
						target_kind = 'invitation_suggestion', target_id = suggestion.id::text,
						target_label = 'Invitation suggestion'
						FROM invitation_suggestions AS suggestion
						WHERE suggestion.id = activity.invitation_suggestion_id`,
					`ALTER TABLE curator_activity_items
						ALTER COLUMN source_kind SET NOT NULL,
						ALTER COLUMN source_id SET NOT NULL,
						ALTER COLUMN version SET NOT NULL,
						ALTER COLUMN category SET NOT NULL`,
					`ALTER TABLE curator_activity_items ADD CONSTRAINT curator_activity_items_category_check CHECK (
						category IN ('security', 'access', 'publication', 'withdrawal', 'comment', 'favorite',
						'invitation_suggestion', 'delivery', 'engagement'))`,
					`CREATE UNIQUE INDEX curator_activity_items_source_version_idx
						ON curator_activity_items (source_kind, source_id, version)`,
					`CREATE INDEX curator_activity_items_category_time_idx
						ON curator_activity_items (category, created_at DESC, id DESC)`,
					`CREATE OR REPLACE FUNCTION memento_project_suggestion_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						NEW.source_kind := 'invitation_suggestion_activity';
						NEW.source_id := NEW.id::text;
						NEW.version := 'suggestion activity ' || NEW.id::text;
						NEW.category := 'invitation_suggestion';
						SELECT requester_person_id INTO NEW.subject_person_id FROM invitation_suggestions WHERE id = NEW.invitation_suggestion_id;
						NEW.target_kind := 'invitation_suggestion';
						NEW.target_id := NEW.invitation_suggestion_id::text;
						NEW.target_label := 'Invitation suggestion';
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER curator_activity_suggestion_projection
						BEFORE INSERT ON curator_activity_items FOR EACH ROW
						WHEN (NEW.source_kind IS NULL) EXECUTE FUNCTION memento_project_suggestion_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_security_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						IF NEW.action LIKE 'invitation_suggestion_%' THEN RETURN NEW; END IF;
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 subject_person_id, outcome)
						VALUES (NEW.actor_person_id, NEW.action, NEW.created_at, 'security_audit', NEW.id::text,
							'audit ' || NEW.id::text,
							CASE WHEN NEW.action IN (
								'pending_recipient_designated', 'invitation_sent', 'invitation_reissued', 'invitation_revoked',
								'invitation_accepted', 'recipient_suspended', 'recipient_suspension_lifted',
								'recipient_access_revoked', 'recipient_email_changed', 'recipient_email_recovered', 'onboarding_completed'
							) THEN 'access' ELSE 'security' END,
							NEW.subject_person_id, NEW.outcome)
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER security_curator_activity_projection
						AFTER INSERT ON security_audit_events FOR EACH ROW EXECUTE FUNCTION memento_project_security_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_publication_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE event_id uuid; event_title text; revision bigint;
					BEGIN
						SELECT publication.event_id, event.title, publication.revision
						INTO event_id, event_title, revision
						FROM publications AS publication JOIN events AS event ON event.id = publication.event_id
						WHERE publication.id = NEW.publication_id;
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 target_kind, target_id, target_label)
						VALUES (NEW.actor_person_id, 'event_published', NEW.created_at, 'publication', NEW.publication_id::text,
							'publication ' || revision::text, 'publication', 'event', event_id::text, event_title)
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER publication_curator_activity_projection
						AFTER INSERT ON publication_curator_activity_items FOR EACH ROW EXECUTE FUNCTION memento_project_publication_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_withdrawal_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE event_title text;
					BEGIN
						IF NEW.action NOT IN ('content_withdrawn', 'content_restored_by_publication') THEN RETURN NEW; END IF;
						SELECT title INTO event_title FROM events WHERE id = NEW.event_id;
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 target_kind, target_id, target_label)
						VALUES (NEW.actor_person_id, NEW.action, NEW.created_at, 'publication_audit', NEW.id::text,
							'publication audit ' || NEW.id::text, 'withdrawal', NEW.target_kind, NEW.target_id::text,
							COALESCE(event_title, 'Content'))
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER withdrawal_curator_activity_projection
						AFTER INSERT ON publication_audit_events FOR EACH ROW EXECUTE FUNCTION memento_project_withdrawal_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_comment_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 target_kind, target_id, target_label)
						VALUES (NEW.author_person_id, 'comment_created', NEW.created_at, 'comment', NEW.id::text,
							'comment ' || NEW.id::text, 'comment', 'media', NEW.media_item_id::text, 'Media item')
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER comment_curator_activity_projection
						AFTER INSERT ON comments FOR EACH ROW EXECUTE FUNCTION memento_project_comment_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_favorite_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						IF NEW.kind <> 'favorite' THEN RETURN NEW; END IF;
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 target_kind, target_id, target_label)
						VALUES (NEW.actor_person_id, NEW.action, NEW.created_at, 'interaction_favorite', NEW.id::text,
							'favorite ' || NEW.id::text, 'favorite', 'media', NEW.media_item_id::text, 'Media item')
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER favorite_curator_activity_projection
						AFTER INSERT ON interaction_activity_items FOR EACH ROW EXECUTE FUNCTION memento_project_favorite_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_delivery_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE recipient_id uuid;
					BEGIN
						SELECT COALESCE(batch_access.person_id, invitation_access.person_id) INTO recipient_id
						FROM delivery_problems AS problem
						LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
						LEFT JOIN recipient_access_generations AS batch_access ON batch_access.id = batch.recipient_access_generation_id
						LEFT JOIN email_deliveries AS delivery ON delivery.id = problem.email_delivery_id
						LEFT JOIN invitations AS invitation ON invitation.id = delivery.invitation_id
						LEFT JOIN recipient_access_generations AS invitation_access ON invitation_access.id = invitation.recipient_access_generation_id
						WHERE problem.id = NEW.id;
						IF TG_OP = 'INSERT' THEN
							INSERT INTO curator_activity_items
								(action, created_at, source_kind, source_id, version, category, subject_person_id,
								 target_kind, target_id, target_label)
							VALUES ('delivery_failed', NEW.created_at, 'delivery_problem', NEW.id::text,
								'problem ' || NEW.id::text, 'delivery', recipient_id,
								'delivery_problem', NEW.id::text, 'Delivery problem')
							ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						ELSIF OLD.resolved_at IS NULL AND NEW.resolved_at IS NOT NULL THEN
							INSERT INTO curator_activity_items
								(action, created_at, source_kind, source_id, version, category, subject_person_id,
								 target_kind, target_id, target_label)
							VALUES ('delivery_problem_resolved', NEW.resolved_at, 'delivery_problem_resolution', NEW.id::text,
								'problem resolution ' || NEW.id::text || ' ' || extract(epoch FROM NEW.resolved_at)::text,
								'delivery', recipient_id, 'delivery_problem', NEW.id::text, 'Delivery problem')
							ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						END IF;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER delivery_curator_activity_projection
						AFTER INSERT OR UPDATE OF resolved_at ON delivery_problems
						FOR EACH ROW EXECUTE FUNCTION memento_project_delivery_activity()`,
					`CREATE OR REPLACE FUNCTION memento_project_engagement_activity() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE label text;
					BEGIN
						IF NEW.kind NOT IN ('session_started', 'visit', 'destination_opened', 'event_opened', 'media_opened', 'video_started',
							'original_download_started', 'archive_download_started') THEN RETURN NEW; END IF;
						IF NEW.event_id IS NOT NULL THEN SELECT title INTO label FROM events WHERE id = NEW.event_id;
						ELSIF NEW.media_item_id IS NOT NULL THEN label := 'Media item';
						ELSIF NEW.destination IS NOT NULL THEN label := initcap(NEW.destination); END IF;
						INSERT INTO curator_activity_items
							(actor_person_id, action, created_at, source_kind, source_id, version, category,
							 target_kind, target_id, target_label)
						VALUES (NEW.recipient_person_id, NEW.kind, NEW.occurred_at, 'engagement', NEW.id::text,
							'engagement ' || NEW.id::text, 'engagement',
							CASE WHEN NEW.event_id IS NOT NULL THEN 'event' WHEN NEW.media_item_id IS NOT NULL THEN 'media'
								 WHEN NEW.destination IS NOT NULL THEN 'destination' ELSE NULL END,
							COALESCE(NEW.event_id::text, NEW.media_item_id::text, NEW.destination), label)
						ON CONFLICT (source_kind, source_id, version) DO NOTHING;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER engagement_curator_activity_projection
						AFTER INSERT ON engagement_events FOR EACH ROW EXECUTE FUNCTION memento_project_engagement_activity()`,
					`CREATE OR REPLACE FUNCTION memento_record_session_engagement() RETURNS trigger LANGUAGE plpgsql AS $$
					DECLARE inserted_id bigint;
					BEGIN
						IF NOT EXISTS (SELECT 1 FROM recipient_access_generations
							WHERE id = NEW.recipient_access_generation_id AND person_id = NEW.person_id AND state = 'completed') THEN
							RETURN NEW;
						END IF;
						INSERT INTO engagement_events
							(recipient_person_id, recipient_access_generation_id, session_id, kind, origin_key, occurred_at)
						VALUES (NEW.person_id, NEW.recipient_access_generation_id, NEW.id, 'session_started',
							'session:' || NEW.id::text, NEW.created_at)
						ON CONFLICT DO NOTHING RETURNING id INTO inserted_id;
						IF inserted_id IS NOT NULL THEN
							INSERT INTO engagement_daily_aggregates
								(recipient_person_id, activity_date, kind, event_count, first_occurred_at, last_occurred_at)
							VALUES (NEW.person_id, NEW.created_at::date, 'session_started', 1, NEW.created_at, NEW.created_at)
							ON CONFLICT (recipient_person_id, activity_date, kind) DO UPDATE
							SET event_count = engagement_daily_aggregates.event_count + 1,
								first_occurred_at = LEAST(engagement_daily_aggregates.first_occurred_at, EXCLUDED.first_occurred_at),
								last_occurred_at = GREATEST(engagement_daily_aggregates.last_occurred_at, EXCLUDED.last_occurred_at);
						END IF;
						RETURN NEW;
					END $$`,
					`CREATE TRIGGER session_engagement_recording
						AFTER INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION memento_record_session_engagement()`,
					`INSERT INTO engagement_events
						(recipient_person_id, recipient_access_generation_id, session_id, kind, origin_key, occurred_at)
					SELECT session.person_id, session.recipient_access_generation_id, session.id, 'session_started',
						'session:' || session.id::text, session.created_at
					FROM sessions AS session JOIN recipient_access_generations AS access
						ON access.id = session.recipient_access_generation_id AND access.person_id = session.person_id
					WHERE access.state = 'completed'
					ON CONFLICT DO NOTHING`,
					`INSERT INTO engagement_daily_aggregates
						(recipient_person_id, activity_date, kind, event_count, first_occurred_at, last_occurred_at)
					SELECT recipient_person_id, occurred_at::date, kind, count(*), min(occurred_at), max(occurred_at)
					FROM engagement_events WHERE kind = 'session_started'
					GROUP BY recipient_person_id, occurred_at::date, kind
					ON CONFLICT (recipient_person_id, activity_date, kind) DO UPDATE
					SET event_count = EXCLUDED.event_count,
						first_occurred_at = EXCLUDED.first_occurred_at,
						last_occurred_at = EXCLUDED.last_occurred_at`,
					`INSERT INTO curator_activity_items
						(actor_person_id, action, created_at, source_kind, source_id, version, category, subject_person_id, outcome)
					SELECT audit.actor_person_id, audit.action, audit.created_at, 'security_audit', audit.id::text,
						'audit ' || audit.id::text,
						CASE WHEN audit.action IN (
							'pending_recipient_designated', 'invitation_sent', 'invitation_reissued', 'invitation_revoked',
							'invitation_accepted', 'recipient_suspended', 'recipient_suspension_lifted',
							'recipient_access_revoked', 'recipient_email_changed', 'recipient_email_recovered', 'onboarding_completed'
						) THEN 'access' ELSE 'security' END,
						audit.subject_person_id, audit.outcome
					FROM security_audit_events AS audit WHERE audit.action NOT LIKE 'invitation_suggestion_%'
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(actor_person_id, action, created_at, source_kind, source_id, version, category,
						 target_kind, target_id, target_label)
					SELECT activity.actor_person_id, 'event_published', activity.created_at, 'publication', publication.id::text,
						'publication ' || publication.revision::text, 'publication', 'event', event.id::text, event.title
					FROM publication_curator_activity_items AS activity
					JOIN publications AS publication ON publication.id = activity.publication_id
					JOIN events AS event ON event.id = publication.event_id
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(actor_person_id, action, created_at, source_kind, source_id, version, category,
						 target_kind, target_id, target_label)
					SELECT audit.actor_person_id, audit.action, audit.created_at, 'publication_audit', audit.id::text,
						'publication audit ' || audit.id::text, 'withdrawal', audit.target_kind, audit.target_id::text,
						COALESCE(event.title, 'Content')
					FROM publication_audit_events AS audit LEFT JOIN events AS event ON event.id = audit.event_id
					WHERE audit.action IN ('content_withdrawn', 'content_restored_by_publication')
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(actor_person_id, action, created_at, source_kind, source_id, version, category,
						 target_kind, target_id, target_label)
					SELECT comment.author_person_id, 'comment_created', comment.created_at, 'comment', comment.id::text,
						'comment ' || comment.id::text, 'comment', 'media', comment.media_item_id::text, 'Media item'
					FROM comments AS comment ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(actor_person_id, action, created_at, source_kind, source_id, version, category,
						 target_kind, target_id, target_label)
					SELECT interaction.actor_person_id, interaction.action, interaction.created_at,
						'interaction_favorite', interaction.id::text, 'favorite ' || interaction.id::text,
						'favorite', 'media', interaction.media_item_id::text, 'Media item'
					FROM interaction_activity_items AS interaction WHERE interaction.kind = 'favorite'
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(action, created_at, source_kind, source_id, version, category, subject_person_id,
						 target_kind, target_id, target_label)
					SELECT 'delivery_failed', problem.created_at, 'delivery_problem', problem.id::text,
						'problem ' || problem.id::text, 'delivery', COALESCE(batch_access.person_id, invitation_access.person_id),
						'delivery_problem', problem.id::text, 'Delivery problem'
					FROM delivery_problems AS problem
					LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
					LEFT JOIN recipient_access_generations AS batch_access ON batch_access.id = batch.recipient_access_generation_id
					LEFT JOIN email_deliveries AS delivery ON delivery.id = problem.email_delivery_id
					LEFT JOIN invitations AS invitation ON invitation.id = delivery.invitation_id
					LEFT JOIN recipient_access_generations AS invitation_access ON invitation_access.id = invitation.recipient_access_generation_id
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(action, created_at, source_kind, source_id, version, category, subject_person_id,
						 target_kind, target_id, target_label)
					SELECT 'delivery_problem_resolved', problem.resolved_at, 'delivery_problem_resolution', problem.id::text,
						'problem resolution ' || problem.id::text || ' ' || extract(epoch FROM problem.resolved_at)::text,
						'delivery', COALESCE(batch_access.person_id, invitation_access.person_id),
						'delivery_problem', problem.id::text, 'Delivery problem'
					FROM delivery_problems AS problem
					LEFT JOIN notification_batches AS batch ON batch.id = problem.notification_batch_id
					LEFT JOIN recipient_access_generations AS batch_access ON batch_access.id = batch.recipient_access_generation_id
					LEFT JOIN email_deliveries AS delivery ON delivery.id = problem.email_delivery_id
					LEFT JOIN invitations AS invitation ON invitation.id = delivery.invitation_id
					LEFT JOIN recipient_access_generations AS invitation_access ON invitation_access.id = invitation.recipient_access_generation_id
					WHERE problem.resolved_at IS NOT NULL
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
					`INSERT INTO curator_activity_items
						(actor_person_id, action, created_at, source_kind, source_id, version, category,
						 target_kind, target_id, target_label)
					SELECT engagement.recipient_person_id, engagement.kind, engagement.occurred_at, 'engagement', engagement.id::text,
						'engagement ' || engagement.id::text, 'engagement',
						CASE WHEN engagement.event_id IS NOT NULL THEN 'event' WHEN engagement.media_item_id IS NOT NULL THEN 'media'
							 WHEN engagement.destination IS NOT NULL THEN 'destination' ELSE NULL END,
						COALESCE(engagement.event_id::text, engagement.media_item_id::text, engagement.destination),
						CASE WHEN engagement.event_id IS NOT NULL THEN event.title WHEN engagement.media_item_id IS NOT NULL THEN 'Media item'
							 WHEN engagement.destination IS NOT NULL THEN initcap(engagement.destination) ELSE NULL END
					FROM engagement_events AS engagement LEFT JOIN events AS event ON event.id = engagement.event_id
					WHERE engagement.kind IN ('session_started', 'visit', 'destination_opened', 'event_opened', 'media_opened', 'video_started',
						'original_download_started', 'archive_download_started')
					ON CONFLICT (source_kind, source_id, version) DO NOTHING`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
		func(ctx context.Context, db *bun.DB) error {
			return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, statement := range []string{
					`DROP TRIGGER session_engagement_recording ON sessions`,
					`DROP FUNCTION memento_record_session_engagement()`,
					`DROP TRIGGER engagement_curator_activity_projection ON engagement_events`,
					`DROP FUNCTION memento_project_engagement_activity()`,
					`DROP TRIGGER delivery_curator_activity_projection ON delivery_problems`,
					`DROP FUNCTION memento_project_delivery_activity()`,
					`DROP TRIGGER favorite_curator_activity_projection ON interaction_activity_items`,
					`DROP FUNCTION memento_project_favorite_activity()`,
					`DROP TRIGGER comment_curator_activity_projection ON comments`,
					`DROP FUNCTION memento_project_comment_activity()`,
					`DROP TRIGGER withdrawal_curator_activity_projection ON publication_audit_events`,
					`DROP FUNCTION memento_project_withdrawal_activity()`,
					`DROP TRIGGER publication_curator_activity_projection ON publication_curator_activity_items`,
					`DROP FUNCTION memento_project_publication_activity()`,
					`DROP TRIGGER security_curator_activity_projection ON security_audit_events`,
					`DROP FUNCTION memento_project_security_activity()`,
					`DROP TRIGGER curator_activity_suggestion_projection ON curator_activity_items`,
					`DROP FUNCTION memento_project_suggestion_activity()`,
					`DELETE FROM curator_activity_items WHERE invitation_suggestion_id IS NULL`,
					`DROP INDEX curator_activity_items_category_time_idx`,
					`DROP INDEX curator_activity_items_source_version_idx`,
					`ALTER TABLE curator_activity_items DROP CONSTRAINT curator_activity_items_category_check`,
					`ALTER TABLE curator_activity_items DROP CONSTRAINT curator_activity_items_action_check`,
					`ALTER TABLE curator_activity_items ADD CONSTRAINT curator_activity_items_action_check CHECK (
						action IN ('invitation_suggestion_submitted', 'invitation_suggestion_withdrawn',
						'invitation_suggestion_accepted', 'invitation_suggestion_rejected'))`,
					`ALTER TABLE curator_activity_items
						DROP COLUMN outcome, DROP COLUMN target_label, DROP COLUMN target_id, DROP COLUMN target_kind,
						DROP COLUMN subject_person_id, DROP COLUMN category, DROP COLUMN version, DROP COLUMN source_id, DROP COLUMN source_kind`,
					`ALTER TABLE curator_activity_items ALTER COLUMN invitation_suggestion_id SET NOT NULL`,
					`ALTER TABLE curator_activity_items ALTER COLUMN actor_person_id SET NOT NULL`,
				} {
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			})
		},
	)
}
