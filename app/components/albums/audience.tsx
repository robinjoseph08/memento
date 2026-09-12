import { initials } from "../../lib/initials";
import type { AccessPerson } from "../../types/generated/publishing";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";

// Avatars shown before the rest collapse into a count.
const shownAvatars = 4;

// Who can see a Moment, at a glance: a short stack of avatars with the
// overflow as a count and the pending suggestions beside it. Hovering the
// stack lists everyone with access; the names are also its accessible label,
// so the outline link that contains it needs no second tab stop.
export function Audience({
  people,
  suggestions,
}: {
  people: AccessPerson[];
  suggestions: number;
}) {
  const allowed = people.filter((person) => person.decision === "allow");
  const shown = allowed.slice(0, shownAvatars);
  const overflow = allowed.length - shown.length;
  const names = allowed.map((person) => person.display_name).join(", ");
  return (
    <span className="flex items-center gap-2 text-xs">
      {allowed.length === 0 ? (
        <span className="text-muted">No access yet</span>
      ) : (
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                aria-label={`Allowed: ${names}`}
                className="flex cursor-default items-center -space-x-1.5 rounded-full"
                role="img"
              >
                {shown.map((person) => (
                  <Avatar
                    className="size-6 border-2 border-background"
                    key={person.person_id}
                  >
                    {person.avatar_url && (
                      <AvatarImage alt="" src={person.avatar_url} />
                    )}
                    <AvatarFallback className="text-[10px]">
                      {initials(person.display_name)}
                    </AvatarFallback>
                  </Avatar>
                ))}
                {overflow > 0 && (
                  <span className="flex size-6 items-center justify-center rounded-full border-2 border-background bg-surface text-[10px] font-medium">
                    +{overflow}
                  </span>
                )}
              </span>
            </TooltipTrigger>
            <TooltipContent align="start" side="bottom">
              <ul className="space-y-1">
                {allowed.map((person) => (
                  <li key={person.person_id}>{person.display_name}</li>
                ))}
              </ul>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>
      )}
      {suggestions > 0 && (
        <span className="text-accent-foreground">{suggestions} suggested</span>
      )}
    </span>
  );
}
