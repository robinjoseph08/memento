import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { InvitationSuggestions } from "./InvitationSuggestions";
import type { SessionResponse } from "./types/generated/setup";

const session: SessionResponse = {
  display_name: "Alex",
  session_type: "trusted",
  csrf_token: "c".repeat(64),
  curator: false,
  onboarding_required: false,
};

function response(json: unknown, status = 200) {
  return new Response(JSON.stringify(json), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestPath(input: RequestInfo | URL) {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.pathname;
  return new URL(input.url).pathname;
}

function requestBody(init?: RequestInit): unknown {
  if (typeof init?.body !== "string") return undefined;
  return JSON.parse(init.body) as unknown;
}

function renderSuggestions(activeSession = session) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <InvitationSuggestions session={activeSession} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("InvitationSuggestions", () => {
  it("submits the required spoken answer and only offers withdrawal while Submitted", async () => {
    const requests: Array<{ path: string; body?: unknown; csrf?: string }> = [];
    let suggestions: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = requestPath(input);
        requests.push({
          path,
          body: requestBody(init),
          csrf: new Headers(init?.headers).get("X-Memento-CSRF") ?? undefined,
        });
        if (path === "/api/invitation-suggestions" && init?.method === "POST") {
          const body = requestBody(init) as Record<string, unknown>;
          suggestions = [
            {
              id: "11111111-1111-4111-8111-111111111111",
              ...body,
              status: "submitted",
              submitted_at: "2026-07-27T12:00:00Z",
            },
          ];
          return Promise.resolve(response(suggestions[0], 201));
        }
        if (path.endsWith("/withdraw")) {
          suggestions = [
            { ...(suggestions[0] as object), status: "withdrawn" },
          ];
          return Promise.resolve(response(suggestions[0]));
        }
        return Promise.resolve(response({ suggestions }));
      }),
    );

    renderSuggestions();
    await screen.findByText("No Invitation suggestions yet.");
    fireEvent.change(screen.getByLabelText("Person's name"), {
      target: { value: "Taylor <script>" },
    });
    fireEvent.change(screen.getByLabelText("Email"), {
      target: { value: "taylor@example.com" },
    });
    fireEvent.change(screen.getByLabelText("Relationship context"), {
      target: { value: "Cousin & family friend" },
    });
    fireEvent.click(screen.getByLabelText("No"));
    fireEvent.click(screen.getByRole("button", { name: "Submit suggestion" }));

    await screen.findByText("Taylor <script>");
    expect(screen.getByText("Submitted")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Withdraw" }),
    ).toBeInTheDocument();
    const submission = requests.find(
      (request) =>
        request.path === "/api/invitation-suggestions" && request.body,
    );
    expect(submission?.body).toMatchObject({
      spoke_with_person: false,
      relationship_context: "Cousin & family friend",
    });
    expect(submission?.csrf).toBe(session.csrf_token);

    fireEvent.click(screen.getByRole("button", { name: "Withdraw" }));
    await screen.findByText("Withdrawn");
    expect(
      screen.queryByRole("button", { name: "Withdraw" }),
    ).not.toBeInTheDocument();
  });

  it("refreshes stale controls when a competing Curator resolution wins", async () => {
    let suggestion = {
      id: "11111111-1111-4111-8111-111111111111",
      name: "Taylor Existing",
      email: "taylor@example.com",
      relationship_context: "Cousin",
      spoke_with_person: true,
      status: "submitted",
      submitted_at: "2026-07-27T12:00:00Z",
    };
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        const path = requestPath(input);
        if (path.endsWith("/withdraw")) {
          suggestion = { ...suggestion, status: "rejected" };
          return Promise.resolve(
            response(
              {
                error: {
                  message:
                    "This Invitation suggestion is no longer Submitted. Refresh before trying again.",
                },
              },
              409,
            ),
          );
        }
        return Promise.resolve(response({ suggestions: [suggestion] }));
      }),
    );

    renderSuggestions();
    fireEvent.click(await screen.findByRole("button", { name: "Withdraw" }));

    await screen.findByText("Rejected");
    expect(
      screen.queryByRole("button", { name: "Withdraw" }),
    ).not.toBeInTheDocument();
  });

  it("lets the Curator explicitly match an existing Person without presenting access as automatic", async () => {
    const acceptedBodies: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const path = requestPath(input);
        if (path === "/api/invitation-suggestions") {
          return response({ suggestions: [] });
        }
        if (path.startsWith("/api/people")) {
          return response({
            people: [
              {
                id: "22222222-2222-4222-8222-222222222222",
                display_name: "Taylor Existing",
                sort_name: "Existing, Taylor",
                version: 1,
                status: "current",
                created_at: "2026-07-27T12:00:00Z",
                updated_at: "2026-07-27T12:00:00Z",
                roles: [],
                unrevoked_sessions: 0,
                historical_audit_count: 0,
              },
            ],
          });
        }
        if (path.endsWith("/accept")) {
          acceptedBodies.push(requestBody(init));
          return response({
            id: "11111111-1111-4111-8111-111111111111",
            status: "accepted",
          });
        }
        return response({
          suggestions: [
            {
              id: "11111111-1111-4111-8111-111111111111",
              requester_person_id: "33333333-3333-4333-8333-333333333333",
              requester_name: "Alex Requester",
              name: "Taylor Existing",
              email: "taylor@example.com",
              relationship_context: "Cousin",
              spoke_with_person: true,
              status: "submitted",
              submitted_at: "2026-07-27T12:00:00Z",
              matching_people: [
                {
                  person_id: "22222222-2222-4222-8222-222222222222",
                  display_name: "Taylor Existing",
                  reasons: ["same_name", "same_recipient_email"],
                },
              ],
              duplicate_suggestion_count: 1,
            },
          ],
        });
      }),
    );

    renderSuggestions({ ...session, curator: true });
    await screen.findByRole("heading", { name: "Taylor Existing" });
    expect(
      screen.getByText(/No identity was matched automatically/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /Acceptance does not designate Recipient access or send an Invitation/,
      ),
    ).toBeInTheDocument();
    const acceptButton = screen.getByRole("button", {
      name: "Match Person and accept",
    });
    expect(acceptButton).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Match an existing Person"), {
      target: { value: "22222222-2222-4222-8222-222222222222" },
    });
    expect(acceptButton).toBeEnabled();
    fireEvent.click(acceptButton);
    await waitFor(() => expect(acceptedBodies).toHaveLength(1));
    expect(acceptedBodies[0]).toEqual({
      person_id: "22222222-2222-4222-8222-222222222222",
    });
  });
});
