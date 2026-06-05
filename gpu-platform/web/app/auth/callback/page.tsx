"use client";
import { Suspense, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth";

// OAuth landing page. Google → control plane callback redirects here with the session
// token in the URL fragment (#token=...), which never reaches a server or logs. We adopt
// it, then route to onboarding (no org yet) or the dashboard.
function Callback() {
  const { loginWithToken } = useAuth();
  const router = useRouter();
  const [error, setError] = useState("");

  useEffect(() => {
    const hash = new URLSearchParams(window.location.hash.replace(/^#/, ""));
    const token = hash.get("token");
    if (!token) {
      router.replace("/login?error=exchange_failed");
      return;
    }
    // Remove the token from the address bar immediately.
    window.history.replaceState(null, "", window.location.pathname);
    loginWithToken(token)
      .then((u) => router.replace(u.onboarded === false ? "/onboarding" : "/overview"))
      .catch(() => setError("We couldn't complete sign-in. Please try again."));
  }, [loginWithToken, router]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-canvas">
      <div className="text-center">
        {error ? (
          <>
            <p className="text-sm text-red-400">{error}</p>
            <a href="/login" className="mt-3 inline-block text-sm text-ink underline">Back to sign in</a>
          </>
        ) : (
          <p className="text-sm text-ink-muted">Signing you in…</p>
        )}
      </div>
    </div>
  );
}

export default function CallbackPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center bg-canvas text-sm text-ink-muted">Signing you in…</div>}>
      <Callback />
    </Suspense>
  );
}
