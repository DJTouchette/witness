from src.api import create_order


def test_create_order(customer):
    assert create_order(customer, 10)["total"] == 10
