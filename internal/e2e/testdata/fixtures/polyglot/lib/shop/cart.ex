defmodule Shop.Cart do
  @moduledoc "Shopping cart totals."

  def total(items) do
    Enum.sum(items)
  end
end
