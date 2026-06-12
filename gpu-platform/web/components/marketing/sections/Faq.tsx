"use client";
// FAQ — fully accessible disclosure accordion: real <button>s with
// aria-expanded/aria-controls, panels as labelled regions, arrow-key support.
// Open/close animates via the CSS grid-rows trick (user-initiated, ≤ --dur-base,
// so it's allowed under reduced motion per the design-system policy).
import { useId, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";
import { useGsap, reveal } from "../useGsap";
import { SectionHeading } from "../SectionHeading";

const FAQS = [
  {
    q: "Does NodeHive run on our hardware or yours?",
    a: "Yours. NodeHive is the control plane; compute stays on GPU machines you own, inside your network. Code, models, and data never leave your perimeter.",
  },
  {
    q: "How do employees actually connect?",
    a: "They pick a template (PyTorch, TensorFlow, CUDA, JupyterLab) and get an SSH or notebook workstation on demand. Sessions work the same from home, the office, or anywhere else.",
  },
  {
    q: "How is access secured?",
    a: "Every connection runs over mTLS, credentials are hashed, access is role-based, and every action lands in the audit log. Contractor access can be scoped and time-boxed.",
  },
  {
    q: "Can we see who is spending what?",
    a: "Yes — usage is metered per user, project, and department, with budgets, credits, and chargeback built in. Finance gets attribution; engineers get autonomy.",
  },
  {
    q: "Do we have to change our ML tooling?",
    a: "No. Workstations are standard Linux environments with GPU passthrough — your existing frameworks, containers, and editors work as-is.",
  },
  {
    q: "How long does rollout take?",
    a: "Installing the agent enrolls a machine in minutes; most teams have their first workstations running the same day. Rollout is per-node, so you can start with one rack.",
  },
];

function FaqItem({ q, a }: { q: string; a: string }) {
  const [open, setOpen] = useState(false);
  const id = useId();
  return (
    <div data-faq className="border-b border-line last:border-b-0">
      <h3>
        <button
          type="button"
          aria-expanded={open}
          aria-controls={`${id}-panel`}
          id={`${id}-button`}
          onClick={() => setOpen((o) => !o)}
          className="flex w-full items-center justify-between gap-4 rounded-md px-1 py-5 text-left font-display text-base font-medium text-ink transition-colors duration-150 hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-indigo/60"
        >
          {q}
          <ChevronDown
            size={18}
            aria-hidden
            className={`shrink-0 transition-[transform,color] duration-200 ease-snappy ${
              open ? "rotate-180 text-brand-cyan" : "text-ink-muted"
            }`}
          />
        </button>
      </h3>
      <div
        id={`${id}-panel`}
        role="region"
        aria-labelledby={`${id}-button`}
        className={`grid transition-[grid-template-rows] duration-200 ease-snappy ${
          open ? "grid-rows-[1fr]" : "grid-rows-[0fr]"
        }`}
      >
        <div className="overflow-hidden">
          <p className="max-w-2xl px-1 pb-5 text-sm leading-relaxed text-ink-muted">{a}</p>
        </div>
      </div>
    </div>
  );
}

export function Faq() {
  const root = useGsap<HTMLElement>(({ gsap, root }) => {
    reveal(gsap, root.querySelectorAll("[data-reveal]"), { trigger: root });
    reveal(gsap, root.querySelectorAll("[data-faq]"), {
      trigger: root.querySelector("[data-list]"),
      y: 20,
    });
  });

  return (
    <section ref={root} id="faq" className="relative scroll-mt-24 py-24 sm:py-28">
      <div className="mx-auto max-w-3xl px-6">
        <SectionHeading
          kicker="FAQ"
          title="Answers for your security review"
          lede="The questions IT and platform leads ask first."
        />
        <div data-list className="mt-12">
          {FAQS.map((f) => (
            <FaqItem key={f.q} {...f} />
          ))}
        </div>
      </div>
    </section>
  );
}
