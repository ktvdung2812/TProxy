import { useEffect, useRef } from "react";

const GLYPHS = "アイウエオカキクケコサシスセソ0123456789ABCDEF<>/\\|=+*#".split("");

/**
 * Decorative "digital rain" canvas rendered behind the app shell.
 * Purely presentational — no interaction, no app state involved.
 */
export function MatrixRain() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const fontSize = 14;
    let columns = 0;
    let drops: number[] = [];
    let rafId = 0;
    let lastFrame = 0;

    const resize = () => {
      canvas.width = window.innerWidth;
      canvas.height = window.innerHeight;
      columns = Math.ceil(canvas.width / fontSize);
      drops = Array.from({ length: columns }, () => Math.floor(Math.random() * -60));
    };

    const draw = (now: number) => {
      rafId = requestAnimationFrame(draw);
      if (now - lastFrame < 66) return; // ~15fps, classic rain cadence
      lastFrame = now;

      ctx.fillStyle = "rgba(2, 5, 3, 0.14)";
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.font = `${fontSize}px "Share Tech Mono", monospace`;

      for (let i = 0; i < columns; i += 1) {
        const glyph = GLYPHS[Math.floor(Math.random() * GLYPHS.length)];
        const x = i * fontSize;
        const y = drops[i] * fontSize;
        ctx.fillStyle = Math.random() > 0.975 ? "#7cc596" : "#328554";
        ctx.fillText(glyph, x, y);
        if (y > canvas.height && Math.random() > 0.985) {
          drops[i] = Math.floor(Math.random() * -30);
        }
        drops[i] += 1;
      }
    };

    resize();
    window.addEventListener("resize", resize);
    rafId = requestAnimationFrame(draw);

    return () => {
      cancelAnimationFrame(rafId);
      window.removeEventListener("resize", resize);
    };
  }, []);

  return <canvas ref={canvasRef} className="matrix-rain" aria-hidden="true" />;
}
