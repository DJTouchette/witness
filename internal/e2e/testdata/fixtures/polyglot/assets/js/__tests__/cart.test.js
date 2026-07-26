import { total } from "../cart.js";

test("sums items", () => {
  expect(total([1, 2])).toBe(3);
});
