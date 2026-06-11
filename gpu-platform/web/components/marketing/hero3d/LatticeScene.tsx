"use client";
// Signature 3D hero (Phase 2): an instanced compute lattice — a calm, waving
// fabric of nodes (the company's GPU fleet) with light pulses traveling the
// edges (secure work delivered to people anywhere). Deliberately quiet and
// infrastructural: no spinning objects, no sci-fi bloom.
//
// This module (and its three/R3F/drei imports) is ONLY ever loaded via
// next/dynamic from Hero3D.tsx — it must never be imported statically.
//
// Performance contract:
//  - DPR capped at 2; drei PerformanceMonitor drops it to 1 on sustained jank.
//  - `active={false}` (off-screen hero or hidden tab) sets frameloop="never".
//  - `lite` halves grid density and pulse count for small viewports.
//  - All geometries/materials are JSX-owned, so R3F disposes them on unmount;
//    the one imperative resource (line positions buffer) is plain typed-array
//    data inside a JSX-owned BufferGeometry — nothing leaks.
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
        gradeColor(i / (cols - 1), c);
        col.set([c.r, c.g, c.b], (e * 2 + k) * 3);
      });
    });
    return { edgePairs: pairs, linePositions: pos, lineColors: col, edgeCount: pairs.length };
  }, [cols, rows]);

  const pulses = useMemo(
    () => Array.from({ length: pulseCount }, () => spawnPulse(cols, rows)),
    [pulseCount, cols, rows],
  );

  // One-time instance colors + dynamic-usage hint for the streamed buffers.
  useLayoutEffect(() => {
    const c = new THREE.Color();
    for (let n = 0; n < count; n++) {
      gradeColor((n % cols) / (cols - 1), c);
      nodes.current.setColorAt(n, c);
    }
    nodes.current.instanceColor!.needsUpdate = true;
    nodes.current.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    pulseMesh.current.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    (lines.current.geometry.getAttribute("position") as THREE.BufferAttribute).setUsage(
      THREE.DynamicDrawUsage,
    );
  }, [count, cols]);

  const m = useMemo(() => new THREE.Matrix4(), []);
  const v = useMemo(() => new THREE.Vector3(), []);

  useFrame((state, delta) => {
    // Clamp delta so a background-tab resume doesn't teleport the animation.
    const dt = Math.min(delta, 0.05);
    clock.current += dt;
    const t = clock.current;
    const posAttr = lines.current.geometry.getAttribute("position") as THREE.BufferAttribute;

    // Nodes ride the wave; line endpoints follow the same field.
    for (let j = 0; j < rows; j++)
      for (let i = 0; i < cols; i++) {
        const n = j * cols + i;
        const x = xOf(i);
        const z = zOf(j);
        m.setPosition(x, waveY(x, z, t), z);
        nodes.current.setMatrixAt(n, m);
      }
    nodes.current.instanceMatrix.needsUpdate = true;

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

    // Pulses traverse a row/column, glowing brightest mid-path.
    pulses.forEach((p, n) => {
      p.t += dt * p.speed;
      if (p.t >= 1) Object.assign(p, spawnPulse(cols, rows));
      const u = p.dir > 0 ? p.t : 1 - p.t;
      const x = p.axis === 0 ? (u - 0.5) * X_SPREAD : xOf(p.line);
      const z = p.axis === 0 ? zOf(p.line) : Z_FAR + u * (Z_NEAR - Z_FAR);
      const s = 0.5 + Math.sin(Math.PI * p.t) * 0.9;
      m.makeScale(s, s, s);
      m.setPosition(x, waveY(x, z, t) + 0.04, z);
      pulseMesh.current.setMatrixAt(n, m);
    });
    pulseMesh.current.instanceMatrix.needsUpdate = true;

    // Pointer parallax — a few hundredths of a radian, eased.
    const target = v.set(-state.pointer.y * 0.045, state.pointer.x * 0.07, 0);
    group.current.rotation.x += (target.x - group.current.rotation.x) * 0.04;
    group.current.rotation.y += (target.y - group.current.rotation.y) * 0.04;
  });

  return (
    <group ref={group} position={[0, -0.4, 0]}>
      <instancedMesh ref={nodes} args={[undefined, undefined, count]}>
        <sphereGeometry args={[0.032, 8, 8]} />
        <meshBasicMaterial toneMapped={false} />
      </instancedMesh>

      <lineSegments ref={lines}>
        <bufferGeometry>
          <bufferAttribute attach="attributes-position" args={[linePositions, 3]} />
          <bufferAttribute attach="attributes-color" args={[lineColors, 3]} />
        </bufferGeometry>
        <lineBasicMaterial
          vertexColors
          transparent
          opacity={0.16}
          blending={THREE.AdditiveBlending}
          depthWrite={false}
          toneMapped={false}
        />
      </lineSegments>

      <instancedMesh ref={pulseMesh} args={[undefined, undefined, pulseCount]}>
        <sphereGeometry args={[0.055, 8, 8]} />
        <meshBasicMaterial
          color="#d6f3ff"
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
      camera={{ position: [0, 2.7, 8.4], fov: 40 }}
      gl={{ antialias: true, alpha: true, powerPreference: "high-performance" }}
      onCreated={({ scene, camera }) => {
        scene.fog = new THREE.Fog(FOG, 7.5, 14.5);
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
