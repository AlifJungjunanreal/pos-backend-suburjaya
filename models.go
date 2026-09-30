package main

import (
	"time"

	"gorm.io/gorm"
)

// Tabel Cabang Toko
type Branch struct {
	ID            int            `gorm:"primaryKey"`
	Name          string         `gorm:"type:varchar(100)" json:"name"`
	Address       string         `gorm:"type:text" json:"address"`
	Phone         string         `gorm:"type:varchar(20)" json:"phone"`
	ReceiptFooter string         `gorm:"type:text" json:"receipt_footer"`
	TaxPercent    float64        `gorm:"default:0" json:"tax_percent"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tabel Pengguna (Pegawai/Owner)
type User struct {
	ID           int            `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID     int            `gorm:"index" json:"branch_id"`
	Branch       Branch         `gorm:"foreignKey:BranchID" json:"-"`
	Username     string         `gorm:"unique;not null" json:"username"`
	PasswordHash string         `gorm:"not null" json:"-"`
	Role         string         `gorm:"type:varchar(20);index;not null" json:"role"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tabel Supplier 
type Supplier struct {
	ID          int            `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string         `gorm:"type:varchar(100);not null" json:"name"`
	Contact     string         `gorm:"type:varchar(50)" json:"contact"`
	Address     string         `gorm:"type:text" json:"address"`
	CurrentDebt float64        `gorm:"default:0" json:"current_debt"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tabel Pelanggan 
type Customer struct {
	ID           int            `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string         `gorm:"type:varchar(100);not null" json:"name"`
	Phone        string         `gorm:"type:varchar(20)" json:"phone"`
	Address      string         `gorm:"type:text" json:"address"`
	CustomerType string         `gorm:"type:varchar(20);not null;default:'umum'" json:"customer_type"`
	CreditLimit  float64        `json:"credit_limit"`
	CurrentDebt  float64        `json:"current_debt"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tabel Kategori Barang
type Category struct {
	ID                  int            `gorm:"primaryKey;autoIncrement" json:"id"`
	Name                string         `gorm:"not null" json:"name"`
	TargetMarginPercent float64        `json:"target_margin_percent"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tabel Barang (Katalog)
type Product struct {
	ID           int            `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string         `gorm:"type:varchar(100);index;not null" json:"name"`
	Unit         string         `gorm:"type:varchar(20)" json:"unit"`
	BranchStocks []BranchStock `json:"branch_stocks" gorm:"foreignKey:ProductID"`
	PriceGeneral float64        `gorm:"not null" json:"price_general"`
	PriceToko    float64        `gorm:"not null" json:"price_toko"`
	BasePrice    float64        `gorm:"not null;default:0" json:"base_price"` // Ini yang akan di-update oleh Moving Average
	CategoryID   int            `gorm:"index" json:"category_id"`
	Category     Category       `gorm:"foreignKey:CategoryID" json:"-"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// Tabel Gudang Fisik per Cabang (Pusat Stok)
type BranchStock struct {
	ID        int     `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID  int     `gorm:"uniqueIndex:idx_branch_product" json:"branch_id"`
	Branch    Branch  `gorm:"foreignKey:BranchID" json:"-"`
	ProductID int     `gorm:"uniqueIndex:idx_branch_product" json:"product_id"`
	Product   Product `gorm:"foreignKey:ProductID" json:"-"`
	Quantity  int     `gorm:"default:0" json:"quantity"`
}

// Tabel Nota Utama
type Transaction struct {
	ID            int       `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID      int       `gorm:"index;not null" json:"branch_id"`
	Branch        Branch    `gorm:"foreignKey:BranchID" json:"-"`
	CashierID     int       `gorm:"index;not null" json:"cashier_id"`
	User          User      `gorm:"foreignKey:CashierID" json:"-"`
	CustomerID    *int      `gorm:"index" json:"customer_id"`
	Customer      Customer  `gorm:"foreignKey:CustomerID" json:"-"`
	PaymentMethod string    `gorm:"type:varchar(20);index;not null" json:"payment_method"`
	TotalAmount   float64   `gorm:"not null" json:"total_amount"`
	CreatedAt     time.Time `gorm:"index;autoCreateTime" json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// 🔥 UPDATE TABEL: Ditambah kolom HppTotal & Profit untuk mengunci laba per transaksi
type TransactionItem struct {
	ID              int         `gorm:"primaryKey"`
	TransactionID   int         `gorm:"index"`
	Transaction     Transaction `gorm:"foreignKey:TransactionID" json:"-"`
	ProductID       int         `gorm:"index"`
	Product         Product     `gorm:"foreignKey:ProductID" json:"-"`
	ItemName        string      `gorm:"type:varchar(150)" json:"item_name"`
	Quantity        int
	PickedUpQty     int
	PickupStatus    string  `gorm:"index"`
	ActualSoldPrice float64
	Subtotal        float64
	HppTotal        float64 `json:"hpp_total"` // Total Modal Asli 
	Profit          float64 `json:"profit"`    // Laba Bersih (Subtotal - HppTotal)
}

// Tabel Riwayat Barang Masuk
type StockEntry struct {
	ID         int       `gorm:"primaryKey"`
	BranchID   int       `gorm:"index"`
	Branch     Branch    `gorm:"foreignKey:BranchID" json:"-"`
	ProductID  int       `gorm:"index"`
	Product    Product   `gorm:"foreignKey:ProductID" json:"-"`
	SupplierID int       `gorm:"index"`
	Supplier   Supplier  `gorm:"foreignKey:SupplierID" json:"-"`
	Quantity   int
	CostPrice  float64
	EntryDate  time.Time `gorm:"column:entry_date;index"`
	ReferenceNo   string    `gorm:"type:varchar(100);default:'-'" json:"reference_no"`
	PaymentMethod string    `gorm:"type:varchar(50);default:'CASH'" json:"payment_method"`
	CreatedBy     int       `gorm:"index" json:"created_by"`
	User          User      `gorm:"foreignKey:CreatedBy" json:"-"`
}

// Tabel Tutup Kasir (Shift Closing)
type ShiftClosing struct {
	ID                 int       `gorm:"primaryKey"`
	BranchID           int       `gorm:"index"`
	Branch             Branch    `gorm:"foreignKey:BranchID" json:"-"`
	CashierID          int       `gorm:"index"`
	User               User      `gorm:"foreignKey:CashierID" json:"-"`
	ClosingDate        time.Time `gorm:"type:timestamptz;index" json:"closing_date"` // Ubah ke timestamptz agar jam akurat
	ExpectedCash       float64   `json:"expected_cash"`
	ActualPhysicalCash float64   `json:"actual_physical_cash"`
	Difference         float64   `json:"difference"`
	TotalOmzet         float64   `json:"total_omzet"`             // 🔥 Tambahan Omzet
	TotalProfit        float64   `json:"total_profit"`            // 🔥 Tambahan Laba Bersih
	SnapshotData       string    `gorm:"type:text" json:"snapshot_data"` // 🔥 Menyimpan 5 Blok (JSON)
	CreatedAt          time.Time `json:"created_at"`
}

// Struct penangkap data (Payload) dari Frontend React
type ShiftClosingRequest struct {
	BranchID           int     `json:"BranchID"`
	ExpectedCash       float64 `json:"ExpectedCash"`
	ActualPhysicalCash float64 `json:"ActualPhysicalCash"`
	Difference         float64 `json:"Difference"`
	TotalOmzet         float64 `json:"TotalOmzet"`
	TotalProfit        float64 `json:"TotalProfit"`
	SnapshotData       string  `json:"SnapshotData"`
}

// Tabel Pelunasan Piutang
type DebtPayment struct {
	ID            int       `gorm:"primaryKey;autoIncrement" json:"id"`
	CustomerID    int       `gorm:"index" json:"customer_id"`
	Customer      Customer  `gorm:"foreignKey:CustomerID" json:"-"`
	BranchID      int       `gorm:"index" json:"branch_id"`
	AmountPaid    float64   `json:"amount_paid"`
	PaymentMethod string    `json:"payment_method"`
	ReceiverName  string    `gorm:"type:varchar(100)" json:"receiver_name"`
	CreatedAt     time.Time `gorm:"index" json:"created_at"`
}

// Tabel Arus Kas 
type CashLedger struct {
	ID              int       `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID        int       `gorm:"index;not null" json:"branch_id"`
	Branch          Branch    `gorm:"foreignKey:BranchID" json:"-"`
	WalletType      string    `gorm:"type:varchar(20);not null" json:"wallet_type"`
	TransactionType string    `gorm:"type:varchar(10);not null" json:"transaction_type"`
	Category        string    `gorm:"type:varchar(50);not null" json:"category"`
	Description     string    `gorm:"not null" json:"description"`
	Amount          float64   `gorm:"not null" json:"amount"`
	CreatedBy       int       `gorm:"index" json:"created_by"`
	User            User      `gorm:"foreignKey:CreatedBy" json:"-"`
	CreatedAt       time.Time `gorm:"index;autoCreateTime" json:"created_at"`
}

// Tabel Log Perubahan Harga
type PriceChangeLog struct {
	ID              int       `gorm:"primaryKey;autoIncrement" json:"id"`
	ProductID       int       `gorm:"index" json:"product_id"`
	Product         Product   `gorm:"foreignKey:ProductID" json:"-"`
	OldPriceGeneral float64   `json:"old_price_general"`
	NewPriceGeneral float64   `json:"new_price_general"`
	OldPriceToko    float64   `json:"old_price_toko"`
	NewPriceToko    float64   `json:"new_price_toko"`
	ChangedBy       int       `json:"changed_by"`
	User            User      `gorm:"foreignKey:ChangedBy" json:"-"`
	CreatedAt       time.Time `gorm:"index;autoCreateTime" json:"created_at"`
}

// Tabel Log Penyesuaian Stok (Stock Opname)
type StockAdjustmentLog struct {
	ID          int       `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID    int       `gorm:"index" json:"branch_id"`
	Branch      Branch    `gorm:"foreignKey:BranchID" json:"-"`
	ProductID   int       `gorm:"index" json:"product_id"`
	Product     Product   `gorm:"foreignKey:ProductID" json:"-"`
	OldQuantity int       `json:"old_quantity"`
	NewQuantity int       `json:"new_quantity"`
	Difference  int       `json:"difference"`
	Reason      string    `gorm:"type:text" json:"reason"`
	AdjustedBy  int       `json:"adjusted_by"`
	User        User      `gorm:"foreignKey:AdjustedBy" json:"-"`
	CreatedAt   time.Time `gorm:"index;autoCreateTime" json:"created_at"`
}

// Tabel Master Data Staff Tetap
type Employee struct {
	ID         int            `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID   int            `gorm:"index;default:1" json:"branch_id"`
	Branch     Branch         `gorm:"foreignKey:BranchID" json:"-"`
	Name       string         `gorm:"type:varchar(100);not null" json:"name"`
	Role       string         `gorm:"type:varchar(50);not null" json:"role"`
	BaseSalary float64        `gorm:"not null" json:"base_salary"`
	CreatedAt  time.Time      `gorm:"index;autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"` // Soft delete
}

// Tabel Riwayat Transaksi Penggajian (Gaji Staff & Upah Kuli)
type WagePayment struct {
	ID          int       `gorm:"primaryKey;autoIncrement" json:"id"`
	BranchID    int       `gorm:"index;default:1" json:"branch_id"`
	Branch      Branch    `gorm:"foreignKey:BranchID" json:"-"`
	WorkerName  string    `gorm:"type:varchar(100);not null" json:"worker_name"`
	WorkerType  string    `gorm:"type:varchar(20);not null" json:"worker_type"` 
	Description string    `gorm:"type:text;not null" json:"description"`       
	Amount      float64   `gorm:"not null" json:"amount"`
	WalletType  string    `gorm:"type:varchar(20);not null" json:"wallet_type"`
	PaymentDate time.Time `gorm:"index;autoCreateTime" json:"payment_date"`
	PaidBy      int       `gorm:"index" json:"paid_by"` 
	User        User      `gorm:"foreignKey:PaidBy" json:"-"`
}