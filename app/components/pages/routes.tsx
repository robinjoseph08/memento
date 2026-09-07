import {
  AccessDeniedPage,
  AppShell,
  CuratorLayout,
  CuratorPage,
  HomePage,
  InstallationLayout,
  PublicLayout,
  SetupPage,
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
            children: [{ index: true, element: <CuratorPage /> }],
          },
          { path: "/access-denied", element: <AccessDeniedPage /> },
          { path: "*", element: <HomePage /> },
        ],
      },
    ],
  },
];
