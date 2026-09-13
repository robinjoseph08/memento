import { AlbumPage } from "../albums/album-detail";
import { CuratorPage } from "../albums/albums";
import { ImportPage } from "../albums/sources";
import { WelcomePage } from "../identity/onboarding";
import { ProfilePage } from "../identity/profile";
import { PeoplePage, PersonPage } from "../people/people";
import { RequestsPage } from "../people/requests";
import { ViewerAlbumList } from "../viewer/album-list";
import {
  AccessDeniedPage,
  AppShell,
  CuratorLayout,
  HomePage,
  InstallationLayout,
  OnboardingLayout,
  PublicLayout,
  SetupPage,
  SignedInLayout,
  SignInPage,
  ViewerLayout,
} from "./layouts";
import { ViewerAlbumPage, ViewerAlbumRedirect } from "./viewer";

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
                  { path: "requests", element: <RequestsPage /> },
                ],
              },
              {
                element: <CuratorLayout compact />,
                children: [{ path: "albums/:id", element: <AlbumPage /> }],
              },
            ],
          },
          {
            element: <SignedInLayout />,
            children: [{ path: "/profile", element: <ProfilePage /> }],
          },
          {
            element: <OnboardingLayout />,
            children: [{ path: "/welcome", element: <WelcomePage /> }],
          },
          {
            element: <ViewerLayout />,
            children: [
              { path: "/albums", element: <ViewerAlbumList /> },
              { path: "/albums/:id", element: <ViewerAlbumRedirect /> },
              {
                path: "/albums/:id/photos",
                element: <ViewerAlbumPage tab="photos" />,
              },
              {
                path: "/albums/:id/photos/:entryID",
                element: <ViewerAlbumPage tab="photos" />,
              },
              {
                path: "/albums/:id/videos",
                element: <ViewerAlbumPage tab="videos" />,
              },
            ],
          },
          { path: "/access-denied", element: <AccessDeniedPage /> },
          { path: "*", element: <HomePage /> },
        ],
      },
    ],
  },
];
