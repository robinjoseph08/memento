import { initials } from "../../lib/initials";
import { Avatar, AvatarFallback, AvatarImage } from "../ui/avatar";

export function PersonAvatar({
  person,
  className,
}: {
  person: { display_name: string; avatar_url: string };
  className?: string;
}) {
  return (
    <Avatar className={className}>
      {person.avatar_url && <AvatarImage alt="" src={person.avatar_url} />}
      <AvatarFallback className="text-[10px]">
        {initials(person.display_name)}
      </AvatarFallback>
    </Avatar>
  );
}
