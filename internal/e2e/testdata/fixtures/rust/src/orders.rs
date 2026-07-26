pub struct Order {
    pub total: u32,
}

pub fn total(orders: &[Order]) -> u32 {
    orders.iter().map(|o| o.total).sum()
}
