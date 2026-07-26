defmodule Shop.CartTest do
  use ExUnit.Case

  test "sums items" do
    assert Shop.Cart.total([1, 2]) == 3
  end
end
