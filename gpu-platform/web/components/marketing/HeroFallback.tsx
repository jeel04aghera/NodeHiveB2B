// Static hero backdrop — pure CSS, zero JS, server-renderable. This is BOTH the
// instant first paint behind the lazy 3D canvas and the permanent visual for
// reduced-motion / no-WebGL / low-power visitors. Layered radial glows echo the
// lattice scene's palette so the swap (when it happens) reads as "the same
// picture coming alive", not a replacement.
export function HeroFallback() {
  return (
    <div aria-hidden className="absolute inset-0 overflow-hidden">
      {/* Ambient brand glows (same hues as --grad-a/b/c, kept dim for AA text). */}
      <div
        className="absolute inset-0"
        style={{
          background: [
            "radial-gradient(52rem 30rem at 50% 118%, rgb(var(--grad-b) / 0.28), transparent 62%)",
            "radial-gradient(40rem 24rem at 12% -12%, rgb(var(--grad-a) / 0.18), transparent 60%)",
            "radial-gradient(38rem 24rem at 88% -6%, rgb(var(--grad-c) / 0.14), transparent 60%)",
          ].join(","),
        }}
      />
      {/* Perspective node-grid suggestion of the lattice (hairlines, no image). */}
      <div
        className="absolute inset-x-0 bottom-0 h-[46%] [mask-image:linear-gradient(to_top,black_30%,transparent)]"
        style={{
          backgroundImage:
            "linear-gradient(rgb(var(--grad-b) / 0.14) 1px, transparent 1px), linear-gradient(90deg, rgb(var(--grad-b) / 0.14) 1px, transparent 1px)",
          backgroundSize: "56px 56px",
          transform: "perspective(900px) rotateX(58deg) translateY(8%) scale(1.4)",
          transformOrigin: "center bottom",
        }}
      />
      {/* Horizon line glow where the grid meets the content. */}
      <div className="absolute inset-x-0 bottom-[44%] h-px bg-gradient-to-r from-transparent via-brand-indigo/40 to-transparent" />
    </div>
  );
}
