import { useRef, useState } from "react";
import { MotionConfig, animate, motion, useMotionValue, useReducedMotion } from "motion/react";

/**
 * Setup step 3 as a toy: drag the phone along the track to pair it.
 * The drag is horizontal only and the knob sets touch-action: pan-y, so a
 * vertical swipe that starts on it still scrolls the page. A click or Enter
 * does the same as the drag.
 */
export default function Pair() {
  const track = useRef<HTMLDivElement>(null);
  const knob = useRef<HTMLButtonElement>(null);
  const x = useMotionValue(0);
  const reduce = useReducedMotion();
  const [paired, setPaired] = useState(false);
  const dragged = useRef(false);

  const range = () => (track.current && knob.current ? track.current.clientWidth - knob.current.offsetWidth : 0);
  const settle = (next: boolean) => {
    setPaired(next);
    const to = next ? range() : 0;
    if (reduce) x.set(to);
    else animate(x, to, { type: "spring", stiffness: 380, damping: 20 });
  };

  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-pair bg-white" aria-labelledby="pair-title">
        <p className="step">Step 3</p>
        <h2 id="pair-title">Pair your phone, once</h2>
        <p>
          Put the computer and the phone on <a href="https://tailscale.com">Tailscale</a>, then enter the pairing code one
          time. Every other device does the same.
        </p>
        <div className="pair-track" ref={track} data-paired={paired}>
          <span className="pair-goal" aria-hidden="true">
            {paired ? "paired" : "drop here"}
          </span>
          <motion.button
            ref={knob}
            type="button"
            className="pair-knob"
            style={{ x }}
            drag="x"
            dragConstraints={track}
            dragElastic={0.08}
            dragMomentum={false}
            whileDrag={{ scale: 1.08, rotate: -4 }}
            onDragStart={() => (dragged.current = true)}
            onDragEnd={() => {
              settle(x.get() > range() * 0.6);
              setTimeout(() => (dragged.current = false), 0);
            }}
            onClick={() => {
              if (!dragged.current) settle(!paired);
            }}
            aria-pressed={paired}
          >
            {paired ? "Unpair" : "Drag me"}
          </motion.button>
        </div>
        <p className="pair-code" aria-hidden="true">
          {"482913".split("").map((d, i) => (
            <span key={i} data-on={paired}>
              {paired ? d : "?"}
            </span>
          ))}
        </p>
        <p className="state" aria-live="polite">
          {paired ? "Paired. You will not be asked on this device again." : "Not paired yet. Drag the phone across, or press it."}
        </p>
      </section>
    </MotionConfig>
  );
}
