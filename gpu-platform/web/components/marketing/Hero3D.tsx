"use client";
// Gate + lifecycle for the signature 3D hero. The R3F scene (three + fiber +
// drei) is code-split behind next/dynamic and only requested when ALL of:
//  - motion allowed (prefers-reduced-motion: no-preference)
//  - WebGL actually available
//  - not a low-power device (saveData / low memory / weak mobile CPU)
//  - the hero has entered the viewport (IntersectionObserver)
// Until (and unless) that happens, the server-rendered HeroFallback is the
// hero — so first paint and LCP never wait on the canvas. Once mounted, the
// scene stays mounted but its render loop is hard-paused (frameloop="never")
// whenever the hero scrolls away or the tab is hidden.
import { useEffect, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { prefersReducedMotion } from "@/lib/motion";
import { HeroFallback } from "./HeroFallback";

const LatticeScene = dynamic(() => import("./hero3d/LatticeScene"), { ssr: false });

function supportsWebGL(): boolean {
  try {
    const c = document.createElement("canvas");
    return !!(c.getContext("webgl2") || c.getContext("webgl"));
  } catch {
    return false;
  }
}

function isLowPower(): boolean {
  const nav = navigator as Navigator & {
    deviceMemory?: number;
    connection?: { saveData?: boolean };
  };
  if (nav.connection?.saveData) return true;
  if (nav.deviceMemory !== undefined && nav.deviceMemory <= 2) return true;
  // Weak CPU + coarse pointer ≈ low-end phone: fallback stays.
  const coarse = window.matchMedia("(pointer: coarse)").matches;
  return coarse && navigator.hardwareConcurrency !== undefined && navigator.hardwareConcurrency <= 3;
}

export function Hero3D({ className }: { className?: string }) {
  const holder = useRef<HTMLDivElement>(null);
  const [mounted, setMounted] = useState(false); // scene requested (once, sticky)
  const [inView, setInView] = useState(false);
  const [tabVisible, setTabVisible] = useState(true);
  const [lite, setLite] = useState(false);

  useEffect(() => {
    if (prefersReducedMotion() || !supportsWebGL() || isLowPower()) return;
    setLite(window.matchMedia("(max-width: 768px)").matches);

    const el = holder.current!;
    const io = new IntersectionObserver(
      ([entry]) => {
        setInView(entry.isIntersecting);
        if (entry.isIntersecting) setMounted(true);
      },
      { rootMargin: "120px" },
    );
    io.observe(el);

    const onVis = () => setTabVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", onVis);
    return () => {
      io.disconnect();
      document.removeEventListener("visibilitychange", onVis);
    };
  }, []);

  return (
    <div ref={holder} aria-hidden className={className}>
      <HeroFallback />
      {mounted && (
        <div className="absolute inset-0 animate-nh-fade-in-slow">
          <LatticeScene active={inView && tabVisible} lite={lite} />
        </div>
      )}
    </div>
  );
}
