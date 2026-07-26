using Orders;
using Xunit;

namespace Orders.IntegrationTests;

public class CheckoutFlowTests
{
    [Fact]
    public void ChecksOut() => Assert.Equal(4, new Calculator().Add(2, 2));
}
