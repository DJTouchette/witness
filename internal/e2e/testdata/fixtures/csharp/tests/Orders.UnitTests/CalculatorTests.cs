using Orders;
using Xunit;

namespace Orders.UnitTests;

public class CalculatorTests
{
    [Fact]
    public void Adds() => Assert.Equal(3, new Calculator().Add(1, 2));
}
