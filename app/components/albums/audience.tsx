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
// overflow as a count. The full list opens on hover or focus unless the
// summary already sits inside a button, where the button's own action shows it.
export function Audience({
  people,
  suggestions,
  interactive,
}: {
  people: AccessPerson[];
  suggestions: number;
  interactive: boolean;
}) {
  const allowed = people.filter((person) => person.decision === "allow");
  const shown = allowed.slice(0, shownAvatars);
  const overflow = allowed.length - shown.length;
  const names = allowed.map((person) => person.display_name).join(", ");
  const stack =
    allowed.length === 0 ? (
      <span className="text-muted">No access yet</span>
    ) : (
      <span
        aria-label={`Allowed: ${names}`}
        className="flex items-center -space-x-2"
        role="img"
      >
        {shown.map((person) => (
          <Avatar
            className="size-7 border-2 border-surface"
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
          <span className="flex size-7 items-center justify-center rounded-full border-2 border-surface bg-background text-[10px] font-medium">
            +{overflow}
          </span>
        )}
      </span>
    );
  const suggested = suggestions > 0 && (
    <span className="text-accent-foreground">{suggestions} suggested</span>
  );
  if (interactive || allowed.length === 0)
    return (
      <span className="flex flex-col items-end gap-1">
        {stack}
        {suggested}
      </span>
    );
  return (
    <TooltipProvider>
      <span className="flex flex-col items-end gap-1">
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="cursor-default rounded-full" tabIndex={0}>
              {stack}
            </span>
          </TooltipTrigger>
          <TooltipContent align="end" side="bottom">
            <ul className="space-y-1">
              {allowed.map((person) => (
                <li key={person.person_id}>{person.display_name}</li>
              ))}
            </ul>
          </TooltipContent>
        </Tooltip>
        {suggested}
      </span>
    </TooltipProvider>
  );
}
