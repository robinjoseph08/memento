import { lazy, Suspense } from "react";

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

// PROTOTYPE. Under `pnpm prototype:viewer`, the member album routes render the
// viewer prototype variants instead of the placeholder Albums page.
const prototypeViewer = import.meta.env.MODE === "prototype";
const ViewerPrototype = prototypeViewer
  ? lazy(() =>
      import("./viewer-prototype/viewer-variants").then((module) => ({
        default: module.ViewerVariants,
      })),
    )
  : null;
const viewerRoutes = ViewerPrototype
  ? [
      {
        element: <SignedInLayout bare />,
        children: [
          {
            path: "/albums/:id?/:tab?/:mediaId?",
            element: (
              <Suspense fallback={null}>
                <ViewerPrototype />
              </Suspense>
            ),
          },
        ],
      },
    ]
  : [];

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
            children: [
              {
                element: <CuratorLayout />,
                children: [
                  { index: true, element: <CuratorPage /> },
                  { path: "import", element: <ImportPage /> },
                  { path: "people", element: <PeoplePage /> },
                  { path: "people/:id", element: <PersonPage /> },
                ],
              },
              {
                element: <CuratorLayout compact />,
                children: [{ path: "albums/:id", element: <AlbumPage /> }],
              },
            ],
          },
          ...viewerRoutes,
          {
            element: <SignedInLayout />,
            children: [
              { path: "/profile", element: <ProfilePage /> },
              ...(prototypeViewer
                ? []
                : [{ path: "/albums", element: <MemberPage /> }]),
            ],
          },
          { path: "/access-denied", element: <AccessDeniedPage /> },
          { path: "*", element: <HomePage /> },
        ],
      },
    ],
  },
];
