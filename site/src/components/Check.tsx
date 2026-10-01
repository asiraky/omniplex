import { useState } from "react";
import { MotionConfig, motion } from "motion/react";

const TOOLS = ["Claude Code", "Codex", "git"];

/** Setup step 2 as a toy: the first-launch check for the tools Omniplex drives. */
export default function Check() {
  const [ran, setRan] = useState(false);
  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-check bg-orange" aria-labelledby="check-title">
        <p className="step">Step 2</p>
        <h2 id="check-title">First launch checks your tools</h2>
        <ul className="checks">
          {TOOLS.map((tool, i) => (
            <li key={tool}>
              <motion.span
                className="check-box"
                data-ok={ran}
                initial={false}
                animate={ran ? { scale: [1, 1.5, 1], rotate: [0, -12, 0] } : { scale: 1, rotate: 0 }}
                transition={{ delay: ran ? i * 0.18 : 0, duration: 0.45 }}
              >
                {ran ? "ok" : "?"}
              </motion.span>
              {tool}
            </li>
          ))}
        </ul>
        <motion.button type="button" className="press" whileTap={{ scale: 0.92 }} onClick={() => setRan(!ran)}>
          {ran ? "Reset" : "Run the check"}
        </motion.button>
        <p className="small">It looks for all three and says what is missing.</p>
      </section>
    </MotionConfig>
  );
}
