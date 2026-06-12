"use client";
// Signature 3D hero (Phase 2, elevated in the craft pass): an instanced compute
// lattice — a calm, waving fabric of nodes (the company's GPU fleet) with light
// pulses traveling the edges (secure work delivered to people anywhere).
// Deliberately quiet and infrastructural: no spinning objects, no sci-fi bloom.
//
// Craft notes (why this reads as a fabric, not particles):
//  - The LINES are the subject; nodes are rivets. Line opacity is high enough
//    to draw the grid, and every vertex color is multiplied by an edge-fade so
//    the lattice dissolves at its borders instead of ending in a hard rectangle.
//  - Nodes are small, slightly varied in size, and dimmed toward the front so
//    they never crowd the headline; a slow diagonal light sweep crosses the
//    field (~14s period) so the surface catches light like brushed metal.
//  - Pulses have two-segment trails (instanced, color-graded) — they read as
//    packets moving along edges, not fireflies.
//
// This module (and its three/R3F/drei imports) is ONLY ever loaded via
// next/dynamic from Hero3D.tsx — it must never be imported statically.
//
// Performance contract (unchanged):
//  - DPR capped at 2; drei PerformanceMonitor drops it to 1 on sustained jank.
//  - `active={false}` (off-screen hero or hidden tab) sets frameloop="never".
//  - `lite` halves grid density and pulse count for small viewports.
//  - All geometries/materials are JSX-owned, so R3F disposes them on unmount.
import { useMemo, useRef, useLayoutEffect } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import { PerformanceMonitor } from "@react-three/drei";
import * as THREE from "three";

// Brand gradient endpoints — mirror --grad-a/b/c in app/globals.css.
const GRAD_A = new THREE.Color("#8b5cf6"); // violet
const GRAD_B = new THREE.Color("#6366f1"); // indigo
const GRAD_C = new THREE.Color("#22d3ee"); // cyan
const FOG = "#0b0b0c"; // --c-canvas

const X_SPREAD = 15; // world-units width of the lattice
const Z_NEAR = 3.2;
const Z_FAR = -5.2;

/** Brand gradient sampled across normalized x (violet → indigo → cyan, like 135°). */
function gradeColor(t: number, out: THREE.Color) {
  if (t < 0.45) return out.copy(GRAD_A).lerp(GRAD_B, t / 0.45);
  return out.copy(GRAD_B).lerp(GRAD_C, (t - 0.45) / 0.55);
}

/**
 * Edge dissolve: 1 in the field, easing to 0 at the x borders and toward the
 * front row (which sits nearest the camera/headline). Multiplied into vertex
 * and instance colors — with additive blending, darker = more transparent.
 */
function edgeFade(u: number, v: number) {
  const x = Math.min(u / 0.18, (1 - u) / 0.18, 1); // side dissolve
  const front = Math.min((1 - v) / 0.22, 1); // v=1 is the front (near) row
  const sx = x * x * (3 - 2 * x);
  const sf = 0.35 + 0.65 * (front * front * (3 - 2 * front));
  return sx * sf;
}

/** The fabric's height field — two slow interfering waves, ~±0.4 units. */
function waveY(x: number, z: number, t: number) {
  return (
    0.26 * Math.sin(x * 0.55 + t * 0.55) * Math.cos(z * 0.48 + t * 0.38) +
    0.14 * Math.sin((x + z) * 0.32 + t * 0.7)
  );
}

interface PulseState {
  axis: 0 | 1; // 0 = travels along x (a row), 1 = along z (a column)
  line: number; // which row/column
  t: number; // 0..1 progress
  speed: number; // progress per second
  dir: 1 | -1;
}

function spawnPulse(cols: number, rows: number): PulseState {
  const axis = Math.random() < 0.6 ? 0 : 1;
  return {
    axis,
    line: Math.floor(Math.random() * (axis === 0 ? rows : cols)),
    t: 0,
    speed: 1 / (3.5 + Math.random() * 4), // 3.5–7.5s per traverse
    dir: Math.random() < 0.5 ? 1 : -1,
  };
}

// Trail: head + 2 ghosts, offset back along the path, dimming toward the tail.
const TRAIL = [
  { lag: 0, scale: 1, intensity: 1 },
  { lag: 0.022, scale: 0.7, intensity: 0.45 },
  { lag: 0.05, scale: 0.45, intensity: 0.18 },
] as const;

function Lattice({ cols, rows, pulseCount }: { cols: number; rows: number; pulseCount: number }) {
  const group = useRef<THREE.Group>(null!);
  const nodes = useRef<THREE.InstancedMesh>(null!);
  const pulseMesh = useRef<THREE.InstancedMesh>(null!);
  const lines = useRef<THREE.LineSegments>(null!);
  const clock = useRef(0);

  const count = cols * rows;
  const xOf = (i: number) => (i / (cols - 1) - 0.5) * X_SPREAD;
  const zOf = (j: number) => Z_FAR + (j / (rows - 1)) * (Z_NEAR - Z_FAR);

  // Static data: edge index pairs + per-vertex colors (positions stream per frame).
  const { edgePairs, linePositions, lineColors, edgeCount } = useMemo(() => {
    const pairs: [number, number][] = [];
    for (let j = 0; j < rows; j++)
      for (let i = 0; i < cols; i++) {
        const a = j * cols + i;
        if (i < cols - 1) pairs.push([a, a + 1]);
        if (j < rows - 1) pairs.push([a, a + cols]);
      }
    const pos = new Float32Array(pairs.length * 2 * 3);
    const col = new Float32Array(pairs.length * 2 * 3);
    const c = new THREE.Color();
    pairs.forEach(([a, b], e) => {
      [a, b].forEach((n, k) => {
        const i = n % cols;
        const j = (n / cols) | 0;
        gradeColor(i / (cols - 1), c);
        c.multiplyScalar(edgeFade(i / (cols - 1), j / (rows - 1)));
        col.set([c.r, c.g, c.b], (e * 2 + k) * 3);
      });
    });
    return { edgePairs: pairs, linePositions: pos, lineColors: col, edgeCount: pairs.length };
  }, [cols, rows]);

  // Per-node identity: base color (graded + edge-faded + slight jitter) and a
  // small deterministic size variance so the field doesn't read as stamped.
  const { nodeBase, nodeScale } = useMemo(() => {
    const base = new Float32Array(count * 3);
    const scale = new Float32Array(count);
    const c = new THREE.Color();
    for (let n = 0; n < count; n++) {
      const i = n % cols;
      const j = (n / cols) | 0;
      // Cheap deterministic hash → stable jitter (no Math.random per frame).
      const h = ((n * 2654435761) >>> 8) % 1000 / 1000;
      gradeColor(i / (cols - 1), c);
      c.multiplyScalar(edgeFade(i / (cols - 1), j / (rows - 1)) * (0.55 + 0.3 * h));
      base.set([c.r, c.g, c.b], n * 3);
      scale[n] = 0.75 + 0.5 * (((n * 1103515245 + 12345) >>> 9) % 1000) / 1000;
    }
    return { nodeBase: base, nodeScale: scale };
  }, [count, cols, rows]);

  const pulses = useMemo(
    () => Array.from({ length: pulseCount }, () => spawnPulse(cols, rows)),
    [pulseCount, cols, rows],
  );

  // One-time setup: instance colors + dynamic-usage hints for streamed buffers.
  useLayoutEffect(() => {
    const c = new THREE.Color();
    for (let n = 0; n < count; n++) {
      c.setRGB(nodeBase[n * 3], nodeBase[n * 3 + 1], nodeBase[n * 3 + 2]);
      nodes.current.setColorAt(n, c);
    }
    nodes.current.instanceColor!.needsUpdate = true;
    nodes.current.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    nodes.current.instanceColor!.setUsage(THREE.DynamicDrawUsage);

    // Pulse trail colors are static per slot (head bright, tail dim).
    const pc = new THREE.Color();
    for (let p = 0; p < pulseCount; p++)
      TRAIL.forEach((seg, s) => {
        pc.set("#d6f3ff").multiplyScalar(seg.intensity);
        pulseMesh.current.setColorAt(p * TRAIL.length + s, pc);
      });
    pulseMesh.current.instanceColor!.needsUpdate = true;
    pulseMesh.current.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    (lines.current.geometry.getAttribute("position") as THREE.BufferAttribute).setUsage(
      THREE.DynamicDrawUsage,
    );
  }, [count, cols, pulseCount, nodeBase]);

  const m = useMemo(() => new THREE.Matrix4(), []);
  const v = useMemo(() => new THREE.Vector3(), []);
  const tmpC = useMemo(() => new THREE.Color(), []);

  useFrame((state, delta) => {
    // Clamp delta so a background-tab resume doesn't teleport the animation.
    const dt = Math.min(delta, 0.05);
    clock.current += dt;
    const t = clock.current;
    const posAttr = lines.current.geometry.getAttribute("position") as THREE.BufferAttribute;

    // Light sweep: a soft diagonal band drifting across the fabric (~14s loop).
    // Nodes inside the band brighten ~35% — the surface catches light.
    const sweep = ((t * 0.07) % 1.4) - 0.2; // normalized sweep center, overshoots edges

    // Nodes ride the wave; brightness follows the sweep.
    for (let j = 0; j < rows; j++)
      for (let i = 0; i < cols; i++) {
        const n = j * cols + i;
        const x = xOf(i);
        const z = zOf(j);
        const u = i / (cols - 1);
        const w = j / (rows - 1);
        const s = nodeScale[n];
        m.makeScale(s, s, s);
        m.setPosition(x, waveY(x, z, t), z);
        nodes.current.setMatrixAt(n, m);

        const d = Math.abs((u + w) * 0.5 - sweep);
        const glow = 1 + Math.max(0, 1 - d / 0.12) * 0.35;
        tmpC.setRGB(nodeBase[n * 3] * glow, nodeBase[n * 3 + 1] * glow, nodeBase[n * 3 + 2] * glow);
        nodes.current.setColorAt(n, tmpC);
      }
    nodes.current.instanceMatrix.needsUpdate = true;
    nodes.current.instanceColor!.needsUpdate = true;

    for (let e = 0; e < edgeCount; e++) {
      const [a, b] = edgePairs[e];
      const ia = a % cols, ja = (a / cols) | 0;
      const ib = b % cols, jb = (b / cols) | 0;
      const xa = xOf(ia), za = zOf(ja);
      const xb = xOf(ib), zb = zOf(jb);
      linePositions.set([xa, waveY(xa, za, t), za, xb, waveY(xb, zb, t), zb], e * 6);
    }
    posAttr.array = linePositions;
    posAttr.needsUpdate = true;

    // Pulses traverse a row/column with a dimming trail, brightest mid-path.
    pulses.forEach((p, n) => {
      p.t += dt * p.speed;
      if (p.t >= 1) Object.assign(p, spawnPulse(cols, rows));
      TRAIL.forEach((seg, si) => {
        const tt = Math.max(0, Math.min(1, p.t - seg.lag));
        const u = p.dir > 0 ? tt : 1 - tt;
        const x = p.axis === 0 ? (u - 0.5) * X_SPREAD : xOf(p.line);
        const z = p.axis === 0 ? zOf(p.line) : Z_FAR + u * (Z_NEAR - Z_FAR);
        const s = (0.5 + Math.sin(Math.PI * p.t) * 0.9) * seg.scale;
        m.makeScale(s, s, s);
        m.setPosition(x, waveY(x, z, t) + 0.04, z);
        pulseMesh.current.setMatrixAt(n * TRAIL.length + si, m);
      });
    });
    pulseMesh.current.instanceMatrix.needsUpdate = true;

    // Pointer parallax — a few hundredths of a radian, eased slowly so the
    // fabric feels heavy (infrastructure, not a toy).
    const target = v.set(-state.pointer.y * 0.045, state.pointer.x * 0.07, 0);
    group.current.rotation.x += (target.x - group.current.rotation.x) * 0.03;
    group.current.rotation.y += (target.y - group.current.rotation.y) * 0.03;
  });

  return (
    <group ref={group} position={[0, -0.4, 0]}>
      <instancedMesh ref={nodes} args={[undefined, undefined, count]}>
        <sphereGeometry args={[0.026, 8, 8]} />
        <meshBasicMaterial
          toneMapped={false}
          transparent
          opacity={0.9}
          blending={THREE.AdditiveBlending}
          depthWrite={false}
        />
      </instancedMesh>

      <lineSegments ref={lines}>
        <bufferGeometry>
          <bufferAttribute attach="attributes-position" args={[linePositions, 3]} />
          <bufferAttribute attach="attributes-color" args={[lineColors, 3]} />
        </bufferGeometry>
        <lineBasicMaterial
          vertexColors
          transparent
          opacity={0.34}
          blending={THREE.AdditiveBlending}
          depthWrite={false}
          toneMapped={false}
        />
      </lineSegments>

      <instancedMesh ref={pulseMesh} args={[undefined, undefined, pulseCount * TRAIL.length]}>
        <sphereGeometry args={[0.05, 8, 8]} />
        <meshBasicMaterial
          transparent
          opacity={0.95}
          blending={THREE.AdditiveBlending}
          depthWrite={false}
          toneMapped={false}
        />
      </instancedMesh>
    </group>
  );
}

/** Drops resolution under sustained jank instead of dropping frames. */
function AdaptiveDpr() {
  const setDpr = useThree((s) => s.setDpr);
  return (
    <PerformanceMonitor
      onDecline={() => setDpr(1)}
      onIncline={() => setDpr(Math.min(window.devicePixelRatio, 2))}
    />
  );
}

export default function LatticeScene({ active, lite }: { active: boolean; lite: boolean }) {
  return (
    <Canvas
      // "never" fully halts the render loop when the hero is off-screen or the
      // tab is hidden — zero GPU/CPU cost while paused.
      frameloop={active ? "always" : "never"}
      dpr={[1, 2]}
      camera={{ position: [0, 2.55, 8.4], fov: 39 }}
      gl={{ antialias: true, alpha: true, powerPreference: "high-performance" }}
      onCreated={({ scene, camera }) => {
        scene.fog = new THREE.Fog(FOG, 7, 14.5);
        camera.lookAt(0, 0.1, 0);
      }}
      style={{ pointerEvents: "none" }}
      eventSource={typeof document !== "undefined" ? document.body : undefined}
    >
      <AdaptiveDpr />
      <Lattice
        cols={lite ? 16 : 26}
        rows={lite ? 10 : 15}
        pulseCount={lite ? 6 : 14}
      />
    </Canvas>
  );
}
