import { headline } from "./page.js";

test("greets", () => {
  expect(headline("world")).toBe("Hello, world");
});
