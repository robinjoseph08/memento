import { AlbumPage } from "../albums/album-detail";
import { CuratorPage } from "../albums/albums";
import { ImportPage } from "../albums/sources";
import { ProfilePage } from "../identity/profile";
import { PeoplePage, PersonPage } from "../people/people";
import {
  AccessDeniedPage,
  AppShell,
  CuratorLayout,
  HomePage,
  InstallationLayout,
  MemberPage,
  PublicLayout,
  SetupPage,
  SignedInLayout,
  SignInPage,
} from "./layouts";

export const routes = [
  {
    element: <AppShell />,
    children: [
      {
        element: <InstallationLayout />,
        children: [
          {
            element: <PublicLayout />,
            children: [
              { path: "/setup", element: <SetupPage /> },
              { path: "/sign-in", element: <SignInPage /> },
            ],
          },
          {
            path: "/curator",
            element: <CuratorLayout />,
            children: [
              { index: true, element: <CuratorPage /> },
              { path: "import", element: <ImportPage /> },
              { path: "albums/:id", element: <AlbumPage /> },
              { path: "people", element: <PeoplePage /> },
              { path: "people/:id", element: <PersonPage /> },
            ],
          },
          {
            element: <SignedInLayout />,
            children: [
              { path: "/profile", element: <ProfilePage /> },
              { path: "/albums", element: <MemberPage /> },
            ],
          },
          { path: "/access-denied", element: <AccessDeniedPage /> },
          { path: "*", element: <HomePage /> },
        ],
      },
    ],
  },
];
