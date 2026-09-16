import { AlbumPage } from "../albums/album-detail";
import { CuratorAlbumsPage } from "../albums/albums";
import { ImportPage } from "../albums/sources";
import { DashboardPage } from "../dashboard/dashboard";
import { WelcomePage } from "../identity/onboarding";
import { ProfilePage } from "../identity/profile";
import { UnsubscribePage } from "../notifications/unsubscribe";
import { UpdatesPage } from "../notifications/updates";
import { PeoplePage, PersonPage } from "../people/people";
import { RequestsPage } from "../people/requests";
import { SettingsPage } from "../settings/settings";
import { ViewerAlbumList } from "../viewer/album-list";
import { NotificationsPage } from "../viewer/notifications";
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
import {
  ViewerAlbumPage,
  ViewerAlbumRedirect,
  ViewerLibraryPage,
  ViewerLibraryRedirect,
} from "./viewer";

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
                  { index: true, element: <DashboardPage /> },
                  { path: "albums", element: <CuratorAlbumsPage /> },
                  { path: "import", element: <ImportPage /> },
                  { path: "import/ignored", element: <ImportPage ignored /> },
                  { path: "people", element: <PeoplePage /> },
                  { path: "people/:id", element: <PersonPage /> },
                  { path: "requests", element: <RequestsPage /> },
                  { path: "updates", element: <UpdatesPage /> },
                  { path: "settings", element: <SettingsPage /> },
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
              { path: "/library", element: <ViewerLibraryRedirect /> },
              {
                path: "/library/photos/:entryID?",
                element: <ViewerLibraryPage tab="photos" />,
              },
              {
                path: "/library/videos/:entryID?",
                element: <ViewerLibraryPage tab="videos" />,
              },
              { path: "/notifications", element: <NotificationsPage /> },
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
              {
                path: "/albums/:id/videos/:entryID",
                element: <ViewerAlbumPage tab="videos" />,
              },
            ],
          },
          { path: "/unsubscribe", element: <UnsubscribePage /> },
          { path: "/access-denied", element: <AccessDeniedPage /> },
          { path: "*", element: <HomePage /> },
        ],
      },
    ],
  },
];
