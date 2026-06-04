"use client";
import { useEffect, useState } from "react";

// The backend is single-org with no org-creation endpoint. The onboarding wizard
// captures the organization profile and we persist it locally so the product can
// reflect "your organization" (name in the sidebar, Settings → General). This is a
// presentation layer on top of the existing single-org backend — no API change.
export interface OrgProfile {
  name: string;
  size?: string;
  useCase?: string;
}

const KEY = "nh_org_profile";
const DEFAULT: OrgProfile = { name: "NodeHive" };

export function saveOrgProfile(p: OrgProfile) {
  try { localStorage.setItem(KEY, JSON.stringify(p)); } catch {}
}

export function readOrgProfile(): OrgProfile {
  try {
    const v = localStorage.getItem(KEY);
    if (v) return { ...DEFAULT, ...JSON.parse(v) };
  } catch {}
  return DEFAULT;
}

export function useOrgProfile(): OrgProfile {
  const [p, setP] = useState<OrgProfile>(DEFAULT);
  useEffect(() => { setP(readOrgProfile()); }, []);
  return p;
}
