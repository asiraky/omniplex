import { useState } from "react";
import { MotionConfig, motion } from "motion/react";

/** A card that flips between the two agents Omniplex drives. */
export default function Flip() {
  const [codex, setCodex] = useState(false);
  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-flip bg-green" aria-labelledby="flip-title">
        <h2 id="flip-title">Claude Code or Codex</h2>
        <button type="button" className="flip" onClick={() => setCodex(!codex)} aria-label={`Showing ${codex ? "Codex" : "Claude Code"}. Flip the card.`}>
          <motion.span
            className="flip-card"
            initial={false}
            animate={{ rotateY: codex ? 180 : 0 }}
            transition={{ type: "spring", stiffness: 200, damping: 15 }}
          >
            <span className="flip-face">Claude Code</span>
            <span className="flip-face flip-back">Codex</span>
          </motion.span>
          <span className="flip-hint">Flip it</span>
        </button>
        <p>Omniplex drives the CLIs you already have, with your own logins. It does not replace them and never sees your tokens.</p>
      </section>
    </MotionConfig>
  );
}
