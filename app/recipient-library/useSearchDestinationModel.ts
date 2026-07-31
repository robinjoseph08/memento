import { useEffect, useState, type FormEvent } from "react";
import {
  NavigationType,
  useLocation,
  useNavigationType,
  useSearchParams,
} from "react-router-dom";

import { useRecipientSearch } from "../hooks/queries/search";
import type { Request as SearchRequest } from "../types/generated/search";
import { classifyRefreshedMedia } from "./mediaPresentation";

type SearchDateKind = "" | "year" | "month" | "date" | "range";
type SearchDateFilter = NonNullable<SearchRequest["date"]>;

export function searchDateKind(value: string): SearchDateKind {
  return value === "year" ||
    value === "month" ||
    value === "date" ||
    value === "range"
    ? value
    : "";
}

function structuredSearchParams(
  kind: SearchDateKind,
  value: string | { start: string; end: string } = "",
) {
  const params = new URLSearchParams();
  if (!kind) return params;
  params.set("date", kind);
  if (kind === "year" && typeof value === "string" && value) {
    params.set("year", value);
  } else if (kind === "month" && typeof value === "string" && value) {
    params.set("month", value);
  } else if (kind === "date" && typeof value === "string" && value) {
    params.set("day", value);
  } else if (kind === "range" && typeof value !== "string") {
    if (value.start) params.set("start", value.start);
    if (value.end) params.set("end", value.end);
  }
  return params;
}

export function useSearchDestinationModel(identityGeneration: string) {
  const location = useLocation();
  const navigationType = useNavigationType();
  const [urlSearchParams, setURLSearchParams] = useSearchParams();
  const onSearchRoute = location.pathname === "/search";
  const [retainedStructuredSearch, setRetainedStructuredSearch] = useState(
    () => (onSearchRoute ? urlSearchParams.toString() : ""),
  );
  const [submittedRequest, setSubmittedRequest] =
    useState<SearchRequest | null>(null);
  const [internalParameterWrite, setInternalParameterWrite] = useState<
    string | null
  >(null);
  const currentStructuredSearch = urlSearchParams.toString();
  const restoringRetainedSearch =
    onSearchRoute &&
    navigationType === NavigationType.Push &&
    !currentStructuredSearch &&
    Boolean(retainedStructuredSearch);

  if (onSearchRoute && internalParameterWrite !== null) {
    if (internalParameterWrite === currentStructuredSearch) {
      setInternalParameterWrite(null);
    }
  } else if (
    onSearchRoute &&
    !restoringRetainedSearch &&
    currentStructuredSearch !== retainedStructuredSearch
  ) {
    setRetainedStructuredSearch(currentStructuredSearch);
    setSubmittedRequest(null);
  }

  useEffect(() => {
    if (restoringRetainedSearch) {
      setURLSearchParams(retainedStructuredSearch, { replace: true });
    }
  }, [restoringRetainedSearch, retainedStructuredSearch, setURLSearchParams]);

  const visibleStructuredSearch = onSearchRoute
    ? restoringRetainedSearch
      ? retainedStructuredSearch
      : currentStructuredSearch
    : retainedStructuredSearch;
  const searchParams = new URLSearchParams(visibleStructuredSearch);
  const [searchText, setSearchTextState] = useState("");
  const search = useRecipientSearch(identityGeneration, submittedRequest);
  const dateKind = searchDateKind(searchParams.get("date") ?? "");
  const searchYear =
    dateKind === "year" ? (searchParams.get("year") ?? "") : "";
  const searchMonth =
    dateKind === "month" ? (searchParams.get("month") ?? "") : "";
  const searchDate = dateKind === "date" ? (searchParams.get("day") ?? "") : "";
  const searchStart =
    dateKind === "range" ? (searchParams.get("start") ?? "") : "";
  const searchEnd = dateKind === "range" ? (searchParams.get("end") ?? "") : "";

  function updateStructuredSearch(next: URLSearchParams) {
    const serialized = next.toString();
    setRetainedStructuredSearch(serialized);
    setSubmittedRequest(null);
    if (onSearchRoute) {
      setInternalParameterWrite(serialized);
      setURLSearchParams(next, { replace: true });
    }
  }

  function setSearchText(value: string) {
    setSubmittedRequest(null);
    setSearchTextState(value);
  }

  function setDateKind(kind: SearchDateKind) {
    updateStructuredSearch(structuredSearchParams(kind));
  }

  function setSearchYear(value: string) {
    updateStructuredSearch(structuredSearchParams("year", value));
  }

  function setSearchMonth(value: string) {
    updateStructuredSearch(structuredSearchParams("month", value));
  }

  function setSearchDate(value: string) {
    updateStructuredSearch(structuredSearchParams("date", value));
  }

  function setSearchStart(value: string) {
    updateStructuredSearch(
      structuredSearchParams("range", { start: value, end: searchEnd }),
    );
  }

  function setSearchEnd(value: string) {
    updateStructuredSearch(
      structuredSearchParams("range", { start: searchStart, end: value }),
    );
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    let date: SearchDateFilter | null = null;
    if (dateKind === "year") {
      date = { kind: "year", year: Number(searchYear) };
    } else if (dateKind === "month") {
      date = { kind: "month", month: searchMonth };
    } else if (dateKind === "date") {
      date = { kind: "date", date: searchDate };
    } else if (dateKind === "range") {
      date = {
        kind: "range",
        start_date: searchStart,
        end_date: searchEnd,
      };
    }
    const nextRequest = { query: searchText, date };
    if (JSON.stringify(nextRequest) === JSON.stringify(submittedRequest)) {
      void search.refetch();
    } else {
      setSubmittedRequest(nextRequest);
    }
  }

  async function refreshListingAccess(mediaID: string) {
    if (!submittedRequest) return "withdrawn" as const;
    const refreshed = await search.refetch();
    if (refreshed.error) return "access-unconfirmed" as const;
    return classifyRefreshedMedia(
      refreshed.data?.photos.find((item) => item.id === mediaID),
    );
  }

  return {
    search,
    searchText,
    setSearchText,
    dateKind,
    setDateKind,
    searchYear,
    setSearchYear,
    searchMonth,
    setSearchMonth,
    searchDate,
    setSearchDate,
    searchStart,
    setSearchStart,
    searchEnd,
    setSearchEnd,
    submitSearch,
    refreshListingAccess,
  };
}

export type SearchDestinationModel = ReturnType<
  typeof useSearchDestinationModel
>;
