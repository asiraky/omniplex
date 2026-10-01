import { useState } from "react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";

const spring = { type: "spring", stiffness: 420, damping: 20 } as const;
const pop = {
  initial: { scale: 0.8, opacity: 0 },
  animate: { scale: 1, opacity: 1 },
  exit: { scale: 0.8, opacity: 0 },
  transition: spring,
};

/** A permission prompt you can answer. Server-rendered in the asking state. */
export default function Permission() {
  const [answer, setAnswer] = useState<"allow" | "deny" | null>(null);

  return (
    <MotionConfig reducedMotion="user">
      <section className="tile tile-ask bg-yellow" aria-labelledby="ask-title">
        <h2 id="ask-title">It asks. You answer.</h2>
        <p>From whichever device you have on you. Try it.</p>
        <div className="prompt" aria-live="polite">
          <AnimatePresence mode="wait" initial={false}>
            {answer === null ? (
              <motion.div key="ask" {...pop}>
                <p className="prompt-who">Claude Code wants to run</p>
                <p className="prompt-cmd">npm test</p>
                <div className="prompt-row">
                  <motion.button type="button" className="press" whileTap={{ scale: 0.92 }} onClick={() => setAnswer("deny")}>
                    Deny
                  </motion.button>
                  <motion.button
                    type="button"
                    className="press press-go"
                    whileTap={{ scale: 0.92 }}
                    onClick={() => setAnswer("allow")}
                  >
                    Allow
                  </motion.button>
                </div>
              </motion.div>
            ) : (
              <motion.div key={answer} {...pop}>
                <p className="prompt-who">{answer === "allow" ? "Allowed from your phone" : "Denied from your phone"}</p>
                <p className="prompt-cmd">
                  {answer === "allow" ? "The agent runs npm test and carries on." : "The agent does not run it. It waits for what you say next."}
                </p>
                <div className="prompt-row">
                  <motion.button type="button" className="press" whileTap={{ scale: 0.92 }} onClick={() => setAnswer(null)}>
                    Ask me again
                  </motion.button>
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </div>
      </section>
    </MotionConfig>
  );
}
