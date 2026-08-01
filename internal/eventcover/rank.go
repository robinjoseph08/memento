// Package eventcover owns the common safe cover ranking used by Event projections.
package eventcover

// ProjectionOrder ranks aliases named media, current, moment, and placement.
// Callers must authorize and withdrawal-filter candidates before applying it.
const ProjectionOrder = `(media.availability = 'current') DESC,
	((current.selected_cover_media_item_id = placement.media_item_id) IS TRUE) DESC,
	((moment.cover_media_item_id = placement.media_item_id) IS TRUE) DESC,
	placement.position,
	placement.media_item_id`

// AuthorizedOrder ranks columns projected by authorization-filtered candidate CTEs.
func AuthorizedOrder(alias string) string {
	return alias + `.available DESC, ` + alias + `.is_selected_cover DESC, ` + alias + `.is_moment_cover DESC, ` + alias + `.position, ` + alias + `.media_item_id`
}
