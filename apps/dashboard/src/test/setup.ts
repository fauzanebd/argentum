// Extends `expect` with the DOM matchers (`toBeInTheDocument`, …) and clears
// the DOM between tests so one component's render cannot be read by the next.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => cleanup());
