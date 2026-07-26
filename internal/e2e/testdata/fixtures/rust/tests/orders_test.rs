use orders::orders::{total, Order};

#[test]
fn sums_totals() {
    assert_eq!(total(&[Order { total: 2 }, Order { total: 3 }]), 5);
}
