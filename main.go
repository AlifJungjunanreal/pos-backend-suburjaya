package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var DB *gorm.DB
var jwtSecret = []byte("kunci_rahasia_skripsi_subur_jaya")

// Konfigurasi WebSocket
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}
var clients = make(map[*websocket.Conn]bool)
var broadcast = make(chan map[string]interface{})
var mutex = &sync.Mutex{}

func handleMessages() {
	for {
		msg := <-broadcast
		mutex.Lock()
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				client.Close()
				delete(clients, client)
			}
		}
		mutex.Unlock()
	}
}

func wsHandler(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	mutex.Lock()
	clients[ws] = true
	mutex.Unlock()

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			mutex.Lock()
			delete(clients, ws)
			mutex.Unlock()
			break
		}
	}
}

func main() {
	// 1. Inisialisasi Koneksi Database PostgreSQL
	dsn := "host=localhost user=postgres password=ikan123qw dbname=pos_subur_jaya port=5432 sslmode=disable TimeZone=Asia/Jakarta"
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Gagal menyambung ke database: ", err)
	}
	fmt.Println("Koneksi Database PostgreSQL Berhasil!")

	// 2. Migrasi Skema Database
	err = DB.AutoMigrate(
		&Branch{}, &User{}, &Category{}, &Product{}, &BranchStock{},
		&DebtPayment{}, &CashLedger{}, &Transaction{}, &TransactionItem{},
		&StockEntry{}, &Supplier{}, &Customer{}, &ShiftClosing{},
		&PriceChangeLog{}, &StockAdjustmentLog{},
		&Employee{}, &WagePayment{},
	)
	if err != nil {
		log.Fatal("Gagal melakukan AutoMigrate tabel: ", err)
	}
	fmt.Println("AutoMigrate Berhasil! Tabel di PostgreSQL tersinkronisasi.")

	var userCount int64
	DB.Model(&User{}).Count(&userCount)
	if userCount == 0 {
		fmt.Println("Database kosong terdeteksi! Membuat Cabang dan Akun Owner default...")

		DB.Create(&Branch{ID: 1, Name: "TB. Subur Jaya", Address: "Bandung"})
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("owner123"), bcrypt.DefaultCost)
		DB.Create(&User{
			BranchID:     1,
			Username:     "owner",
			PasswordHash: string(hashedPassword),
			Role:         "owner",
		})
		fmt.Println("✅ Akun Master berhasil dibuat! (Username: owner | Pass: owner123)")
	}

	// 3. Konfigurasi Router (Gin) & CORS
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:3000"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		// 👇 Terdapat penambahan "Accept" di sini agar file Excel diizinkan masuk
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/api/ping", func(c *gin.Context) { c.JSON(200, gin.H{"message": "Backend Golang siap!"}) })

	// Modul Master Data & Inventori
	r.GET("/api/products", getProducts)
	r.POST("/api/products", createProduct)
	r.POST("/api/products/bulk-upload", AuthMiddleware("owner", "kasir"), bulkUploadProducts)
	r.PUT("/api/products/:id/price", AuthMiddleware("owner", "kasir"), updateProductPrice)
	r.GET("/api/products/:id/logs", AuthMiddleware("owner", "kasir"), getProductPriceLogs)
	r.POST("/api/stock-adjustments", AuthMiddleware("owner", "kasir"), adjustStock)
	r.GET("/api/stock-adjustments", AuthMiddleware("owner", "kasir"), getStockAdjustments)
	r.GET("/api/categories", getCategories)
	r.POST("/api/categories", AuthMiddleware("owner", "kasir"), func(c *gin.Context) {
		var input struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(400, gin.H{"error": "Format data salah"})
			return
		}

		newCat := Category{Name: input.Name}
		if err := DB.Create(&newCat).Error; err != nil {
			c.JSON(500, gin.H{"error": "Gagal menyimpan kategori ke database"})
			return
		}

		c.JSON(200, gin.H{"message": "Kategori berhasil dibuat", "data": newCat})
	})

	// Modul Shift Closing (Telah dikembalikan posisinya!)
	r.POST("/api/shift-closing", AuthMiddleware("kasir", "owner"), submitShiftClosing)
	r.GET("/api/shift-summary", AuthMiddleware("kasir", "owner"), getShiftSummary)
	r.GET("/api/shift-history", AuthMiddleware("kasir", "owner"), getShiftHistory)

	// Modul Supplier
	r.GET("/api/suppliers", getSuppliers)
	r.POST("/api/suppliers", createSupplier)
	r.PUT("/api/suppliers/:id", AuthMiddleware("owner", "kasir"), updateSupplier)
	r.DELETE("/api/suppliers/:id", AuthMiddleware("owner"), deleteSupplier)
	r.POST("/api/suppliers/pay", AuthMiddleware("owner", "kasir"), paySupplierDebt)

	r.POST("/api/stock-entries", AuthMiddleware("kasir", "owner"), addStock)
	r.GET("/api/stock-history", getStockHistory)

	// Modul HRD & Penggajian
	r.GET("/api/employees", AuthMiddleware("owner", "kasir"), getEmployees)
	r.POST("/api/employees", AuthMiddleware("owner", "kasir"), createEmployee)
	r.POST("/api/pay-salary", AuthMiddleware("owner", "kasir"), paySalary)
	r.GET("/api/wages", AuthMiddleware("owner", "kasir"), getWages)
	r.POST("/api/pay-wages", AuthMiddleware("owner", "kasir"), payWages)

	// Modul Kasir & Pelanggan
	r.POST("/api/checkout", AuthMiddleware("kasir", "owner"), checkout)
	r.GET("/api/transactions", AuthMiddleware("owner", "kasir"), getTransactions)
	r.POST("/api/transactions/:id/void", AuthMiddleware("owner", "kasir"), voidTransaction)
	r.GET("/api/kasbon", AuthMiddleware("kasir", "owner"), getKasbonList)
	r.GET("/api/customers", AuthMiddleware("kasir", "owner"), getCustomers)
	r.PUT("/api/customers/:id", AuthMiddleware("kasir", "owner"), updateCustomer)
	r.DELETE("/api/customers/:id", AuthMiddleware("owner"), deleteCustomer)
	r.POST("/api/customers", AuthMiddleware("kasir", "owner"), createCustomer)
	r.POST("/api/debt-payments", AuthMiddleware("kasir", "owner"), payDebt)
	r.GET("/api/debt-history", AuthMiddleware("kasir", "owner"), getDebtHistory)

	// Modul Pengaturan Toko (Multi Cabang)
	r.GET("/api/branches", AuthMiddleware("owner", "kasir", "admin"), func(c *gin.Context) {
		var branches []Branch
		if err := DB.Order("id asc").Find(&branches).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengambil data cabang"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": branches})
	})

	r.POST("/api/branches", AuthMiddleware("owner"), func(c *gin.Context) {
		var count int64
		DB.Model(&Branch{}).Count(&count)
		if count >= 3 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Batas maksimal 3 cabang telah tercapai! Hapus cabang lain jika ingin menambah."})
			return
		}

		var input struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			Phone   string `json:"phone"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
			return
		}
		newBranch := Branch{
			Name:          input.Name,
			Address:       input.Address,
			Phone:         input.Phone,
			ReceiptFooter: "Terima kasih telah berbelanja! Barang yang sudah dibeli tidak dapat ditukar/dikembalikan.",
			TaxPercent:    0,
		}
		if err := DB.Create(&newBranch).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan cabang baru"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Cabang baru berhasil ditambahkan!", "data": newBranch})
	})

	r.PUT("/api/branches/:id", AuthMiddleware("owner"), func(c *gin.Context) {
		id := c.Param("id")
		var input struct {
			Name          string  `json:"name"`
			Address       string  `json:"address"`
			Phone         string  `json:"phone"`
			ReceiptFooter string  `json:"receipt_footer"`
			TaxPercent    float64 `json:"tax_percent"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
			return
		}

		if err := DB.Model(&Branch{}).Where("id = ?", id).Updates(map[string]interface{}{
			"name":           input.Name,
			"address":        input.Address,
			"phone":          input.Phone,
			"receipt_footer": input.ReceiptFooter,
			"tax_percent":    input.TaxPercent,
		}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memperbarui pengaturan cabang"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Pengaturan cabang berhasil diperbarui!"})
	})

	r.DELETE("/api/branches/:id", AuthMiddleware("owner"), func(c *gin.Context) {
		id := c.Param("id")
		if id == "1" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Akses Ditolak! Cabang Utama (Pusat) tidak boleh dihapus."})
			return
		}
		if err := DB.Where("id = ?", id).Delete(&Branch{}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus cabang"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Cabang berhasil dihapus dari sistem!"})
	})

	r.GET("/api/settings", AuthMiddleware("owner", "kasir"), func(c *gin.Context) {
		branchID := c.DefaultQuery("branch_id", "1")
		var branch Branch
		DB.First(&branch, branchID)
		c.JSON(http.StatusOK, gin.H{"data": branch})
	})

	// Modul Keuangan & Analytics
	r.GET("/api/cash-ledger", AuthMiddleware("kasir", "owner"), getCashLedger)
	r.POST("/api/cash-ledger", AuthMiddleware("kasir", "owner"), createCashLedger)
	r.GET("/api/dashboard", AuthMiddleware("owner"), getDashboard)

	// Modul Keamanan
	r.POST("/api/register", register)
	r.POST("/api/login", login)

	go handleMessages()
	r.GET("/ws", wsHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	r.Run(":" + port)
}

// ==========================================
// MODUL KEAMANAN (AUTHENTICATION)
// ==========================================
type AuthInput struct {
	BranchID int    `json:"BranchID"`
	Username string `json:"Username"`
	Password string `json:"Password"`
	Role     string `json:"Role"`
}

func register(c *gin.Context) {
	var input AuthInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengenkripsi password"})
		return
	}

	user := User{
		BranchID:     input.BranchID,
		Username:     input.Username,
		PasswordHash: string(hashedPassword),
		Role:         input.Role,
	}

	if err := DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username sudah terpakai atau gagal menyimpan"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": input.Role + " berhasil didaftarkan!"})
}

func login(c *gin.Context) {
	var input AuthInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	var user User
	if err := DB.Where("username = ?", input.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Username tidak ditemukan"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Password salah!"})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id":   user.ID,
		"role":      user.Role,
		"username":  user.Username,
		"branch_id": user.BranchID, 
		"exp":       time.Now().Add(time.Hour * 24).Unix(),
	})

	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membuat token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Login sukses!", "token": tokenString, "role": user.Role, "branch_id": user.BranchID})
}

func AuthMiddleware(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Akses ditolak! Anda belum login."})
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Tiket Token tidak valid atau sudah kedaluwarsa!"})
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Gagal membaca data tiket"})
			c.Abort()
			return
		}

		userRole := claims["role"].(string)
		c.Set("user_id", int(claims["user_id"].(float64)))
		c.Set("username", claims["username"])
		c.Set("token_branch_id", int(claims["branch_id"].(float64)))

		roleValid := false
		for _, role := range allowedRoles {
			if userRole == role {
				roleValid = true
				break
			}
		}

		if !roleValid {
			c.JSON(http.StatusForbidden, gin.H{"error": "Akses Ditolak! Menu ini tidak dapat diakses oleh: " + userRole})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ==========================================
// MODUL INVENTORI & MASTER DATA
// ==========================================

func getProducts(c *gin.Context) {
	type StockDetail struct {
		BranchID   int    `json:"branch_id"`
		BranchName string `json:"branch_name"` 
		Quantity   int    `json:"quantity"`
	}

	type ProductResponse struct {
		Product
		TotalStockSemuaCabang int           `json:"total_stock_semua_cabang"`
		RincianStokCabang     []StockDetail `json:"rincian_stok_cabang"`
	}

	var products []Product
	if err := DB.Find(&products).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []ProductResponse
	for _, p := range products {
		var branchStocks []BranchStock
		DB.Preload("Branch").Where("product_id = ?", p.ID).Find(&branchStocks)

		var details []StockDetail
		total := 0

		for _, bs := range branchStocks {
			branchName := bs.Branch.Name
			if branchName == "" {
				branchName = fmt.Sprintf("Cabang %d", bs.BranchID)
			}

			details = append(details, StockDetail{
				BranchID:   bs.BranchID,
				BranchName: branchName, 
				Quantity:   bs.Quantity,
			})
			total += bs.Quantity
		}

		response = append(response, ProductResponse{
			Product:               p,
			TotalStockSemuaCabang: total,
			RincianStokCabang:     details,
		})
	}

	c.JSON(http.StatusOK, gin.H{"message": "Katalog Berhasil Ditarik", "data": response})
}

func createProduct(c *gin.Context) {
	var input Product
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	if input.PriceToko == 0 {
		input.PriceToko = input.PriceGeneral - (input.PriceGeneral * 0.10)
	}

	if err := DB.Create(&input).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan barang"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "Barang berhasil ditambahkan!", "data": input})
}

func getCategories(c *gin.Context) {
	var categories []Category
	if err := DB.Find(&categories).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengambil kategori"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": categories})
}

// ==========================================
// MODUL SUPPLIER
// ==========================================

func getSuppliers(c *gin.Context) {
	type SupplierResponse struct {
		ID          int       `json:"id"`
		Name        string    `json:"name"`
		Contact     string    `json:"contact"`
		Address     string    `json:"address"`
		CurrentDebt float64   `json:"current_debt"`
		CreatedAt   time.Time `json:"created_at"`
		TotalOrder  float64   `json:"total_order"`
		Frequency   int       `json:"frequency"`
	}

	var results []SupplierResponse

	err := DB.Table("suppliers").
		Select(`suppliers.id, suppliers.name, suppliers.contact, suppliers.address, suppliers.current_debt, suppliers.created_at,
				COALESCE(SUM(se.quantity * se.cost_price), 0) as total_order,
				COUNT(se.id) as frequency`).
		Where("suppliers.deleted_at IS NULL").
		Joins("LEFT JOIN stock_entries se ON se.supplier_id = suppliers.id").
		Group("suppliers.id, suppliers.name, suppliers.contact, suppliers.address, suppliers.current_debt, suppliers.created_at").
		Order("suppliers.created_at DESC").
		Scan(&results).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengambil data supplier"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Daftar Supplier Berhasil Ditarik", "data": results})
}

func createSupplier(c *gin.Context) {
	var input Supplier
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}
	if err := DB.Create(&input).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan supplier"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "Supplier berhasil ditambahkan!", "data": input})
}

func updateSupplier(c *gin.Context) {
	supplierID := c.Param("id")
	var input struct {
		Name    string `json:"name"`
		Contact string `json:"contact"`
		Address string `json:"address"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	if err := DB.Model(&Supplier{}).Where("id = ?", supplierID).Updates(Supplier{
		Name:    input.Name,
		Contact: input.Contact,
		Address: input.Address,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengupdate data pabrik"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Data Supplier berhasil diperbarui!"})
}

func deleteSupplier(c *gin.Context) {
	supplierID := c.Param("id")

	var supplier Supplier
	if err := DB.First(&supplier, supplierID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Supplier tidak ditemukan"})
		return
	}

	if supplier.CurrentDebt > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dilarang menghapus! Masih ada hutang berjalan di supplier ini."})
		return
	}

	if err := DB.Where("id = ?", supplierID).Delete(&Supplier{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus supplier"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Supplier berhasil dihapus!"})
}

// API UNTUK BAYAR HUTANG PABRIK
func paySupplierDebt(c *gin.Context) {
	var req struct {
		BranchID      int     `json:"branch_id"`
		SupplierID    int     `json:"supplier_id"`
		AmountPaid    float64 `json:"amount_paid"`
		PaymentMethod string  `json:"payment_method"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. Kunci dan Kurangi Hutang Supplier
		var supplier Supplier
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&supplier, req.SupplierID).Error; err != nil {
			return fmt.Errorf("Supplier tidak ditemukan")
		}

		if req.AmountPaid > supplier.CurrentDebt {
			return fmt.Errorf("Nominal bayar melebihi total hutang")
		}

		if err := tx.Model(&supplier).UpdateColumn("current_debt", gorm.Expr("current_debt - ?", req.AmountPaid)).Error; err != nil {
			return err
		}

		// 2. Potong Uang di Buku Kas (Cash Ledger)
		walletType := "CASH"
		if req.PaymentMethod != "tunai" {
			walletType = "BANK"
		}

		cashOut := CashLedger{
			BranchID:        req.BranchID,
			WalletType:      walletType,
			TransactionType: "OUT",
			Category:        "Lain-lain", // Masuk kategori pengeluaran lain
			Description:     fmt.Sprintf("Pelunasan Hutang Pabrik: %s (via %s)", supplier.Name, req.PaymentMethod),
			Amount:          req.AmountPaid,
			CreatedBy:       userID.(int),
		}

		if err := tx.Create(&cashOut).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Beritahu layar lain (Dashboard/Buku Kas) agar update
	go func() {
		broadcast <- map[string]interface{}{"type": "CASH_UPDATE", "waktu": time.Now().Format("15:04:05")}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Pelunasan hutang pabrik berhasil dicatat!"})
}

// ==========================================
// MODUL BARANG MASUK DENGAN MOVING AVERAGE 
// ==========================================
type RestockRequest struct {
	BranchID   int     `json:"BranchID"`
	ProductID  int     `json:"ProductID"`
	SupplierID int     `json:"SupplierID"`
	Quantity   int     `json:"Quantity"`
	CostPrice  float64 `json:"CostPrice"`
	WalletType string  `json:"WalletType"`
	TempoDays  int     `json:"TempoDays"`
	ReferenceNo string  `json:"ReferenceNo"`
}

func addStock(c *gin.Context) {
	var input RestockRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. Simpan riwayat masuk
		entry := StockEntry{
			BranchID:   input.BranchID,
			ProductID:  input.ProductID,
			SupplierID: input.SupplierID,
			Quantity:   input.Quantity,
			CostPrice:  input.CostPrice,
			EntryDate:  time.Now(),
			ReferenceNo:   input.ReferenceNo,
			PaymentMethod: input.WalletType,
			CreatedBy:     userID.(int),
		}
		if err := tx.Create(&entry).Error; err != nil {
			return err
		}

		// 2. Kunci Row Product untuk kalkulasi Moving Average
		var product Product
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&product, input.ProductID).Error; err != nil {
			return err
		}

		// 3. Hitung Total Stok Agregat (Seluruh Cabang) untuk Rata-rata
		var totalOldQuantity int64
		tx.Model(&BranchStock{}).Where("product_id = ?", input.ProductID).Select("COALESCE(SUM(quantity), 0)").Scan(&totalOldQuantity)

		// 🔥 4. RUMUS MOVING AVERAGE (HPP BARU)
		oldTotalValue := float64(totalOldQuantity) * product.BasePrice
		newIncomingValue := float64(input.Quantity) * input.CostPrice
		newTotalQuantity := float64(totalOldQuantity) + float64(input.Quantity)

		var newAverageBasePrice float64 = 0
		if newTotalQuantity > 0 {
			newAverageBasePrice = (oldTotalValue + newIncomingValue) / newTotalQuantity
		}

		// 5. Update BasePrice (Modal Rata-rata) ke Master Product
		if err := tx.Model(&product).Update("base_price", newAverageBasePrice).Error; err != nil {
			return err
		}

		// 6. Update Fisik Gudang Cabang (Upsert)
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "branch_id"}, {Name: "product_id"}}, 
			DoUpdates: clause.Assignments(map[string]interface{}{
				"quantity": gorm.Expr("branch_stocks.quantity + EXCLUDED.quantity"),
			}),
		}).Create(&BranchStock{
			BranchID:  input.BranchID,
			ProductID: input.ProductID,
			Quantity:  input.Quantity,
		}).Error; err != nil {
			return err
		}

		// 7. Logika Keuangan
		totalBiaya := float64(input.Quantity) * input.CostPrice

		if input.WalletType == "TEMPO" {
			if err := tx.Model(&Supplier{}).Where("id = ?", input.SupplierID).
				UpdateColumn("current_debt", gorm.Expr("current_debt + ?", totalBiaya)).Error; err != nil {
				return err
			}
		} else {
			selectedWallet := input.WalletType
			if selectedWallet != "CASH" && selectedWallet != "BANK" {
				selectedWallet = "BANK"
			}

			sumberTeks := "Laci Kasir"
			if selectedWallet == "BANK" {
				sumberTeks = "Rekening Bank"
			}

			cashOut := CashLedger{
				BranchID:        input.BranchID,
				WalletType:      selectedWallet,
				TransactionType: "OUT",
				Category:        "Beli Stok",
				Description:     fmt.Sprintf("Restock Gudang: %s (%d unit) via %s", product.Name, input.Quantity, sumberTeks),
				Amount:          totalBiaya,
				CreatedBy:       userID.(int),
			}

			if err := tx.Create(&cashOut).Error; err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mencatat barang masuk: " + err.Error()})
		return
	}

	go func() {
		broadcast <- map[string]interface{}{"type": "STOCK_UPDATE", "pesan": "Stok bertambah & HPP Rata-rata diupdate!"}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Barang masuk sukses dicatat!"})
}

func getStockHistory(c *gin.Context) {
	type HistoryResult struct {
		ID           int       `json:"id"`
		ProductName  string    `json:"product_name"`
		SupplierName string    `json:"supplier_name"`
		Quantity     int       `json:"quantity"`
		TotalValue   float64   `json:"total_value"`
		EntryDate    time.Time `json:"entry_date"`
		ReferenceNo   string    `json:"reference_no"`   
		PaymentMethod string    `json:"payment_method"` 
		UserName      string    `json:"user_name"`      
	}

	var results []HistoryResult
	
	// Query SQL dimodifikasi untuk menarik kolom baru dan JOIN tabel users
	DB.Table("stock_entries").
		Select(`stock_entries.id, products.name as product_name, suppliers.name as supplier_name, 
		        stock_entries.quantity, (stock_entries.quantity * stock_entries.cost_price) as total_value, 
				stock_entries.entry_date, stock_entries.reference_no, stock_entries.payment_method, 
				users.username as user_name`).
		Joins("left join products on products.id = stock_entries.product_id").
		Joins("left join suppliers on suppliers.id = stock_entries.supplier_id").
		Joins("left join users on users.id = stock_entries.created_by").
		Order("stock_entries.entry_date desc").
		Limit(50).
		Scan(&results)

	c.JSON(http.StatusOK, gin.H{"data": results})
}

// ==========================================
// 🔥 MODUL KASIR (MENGUNCI LABA & SPLIT PAYMENT)
// ==========================================
type CheckoutRequest struct {
	BranchID      int     `json:"BranchID"`
	CashierID     int     `json:"CashierID"`
	PaymentMethod string  `json:"PaymentMethod"` // tunai, transfer, qris, kasbon, split
	CustomerID    *int    `json:"CustomerID"`
	DownPayment   float64 `json:"DownPayment"`
	Items         []struct {
		ProductID      int     `json:"ProductID"`
		Quantity       int     `json:"Quantity"`
		BargainedPrice float64 `json:"BargainedPrice"`
		CustomName     string  `json:"CustomName"`
		ModalPrice     float64 `json:"ModalPrice"` // 🔥 TAMBAHAN PIPA BARU UNTUK HPP MANUAL
	} `json:"Items"`
}

func checkout(c *gin.Context) {
	var req CheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data keranjang salah"})
		return
	}

	if (req.PaymentMethod == "kasbon" || req.PaymentMethod == "split") && req.CustomerID == nil {
		// Pengecualian: Jika split lunas (kombinasi) tanpa pelanggan, kita izinkan.
		// Jika split ngutang (DP < Tagihan), wajib ada pelanggan.
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var subTotalAmount float64

		var customer Customer
		if req.CustomerID != nil {
			if err := tx.First(&customer, *req.CustomerID).Error; err != nil {
				return fmt.Errorf("data pelanggan tidak ditemukan")
			}
		}

		var branchSettings Branch
		tx.FirstOrCreate(&branchSettings, Branch{ID: req.BranchID})

		transaction := Transaction{
			BranchID:      req.BranchID,
			CashierID:     req.CashierID,
			CustomerID:    req.CustomerID,
			PaymentMethod: req.PaymentMethod,
			TotalAmount:   0,
		}

		if err := tx.Create(&transaction).Error; err != nil {
			return err
		}

		for _, item := range req.Items {
			var product Product
			var actualPrice float64
			var totalModalHPP float64
			var finalItemName string

			// 🔥 LOGIKA BYPASS BARANG DADAKAN (NON-KATALOG)
			if item.ProductID <= 0 {
				var manualCategory Category
				if err := tx.Where("name = ?", "Lain-lain").FirstOrCreate(&manualCategory, Category{
					Name: "Lain-lain",
				}).Error; err != nil {
					return err
				}

				if err := tx.Where("name = ?", "Barang Manual (Lain-lain)").FirstOrCreate(&product, Product{
					Name:       "Barang Manual (Lain-lain)",
					Unit:       "pcs",
					CategoryID: manualCategory.ID,
				}).Error; err != nil {
					return err
				}

				actualPrice = item.BargainedPrice
				// 🔥 PERBAIKAN: Kalikan Modal inputan kasir dengan Qty
				totalModalHPP = item.ModalPrice * float64(item.Quantity) 
				
				finalItemName = item.CustomName
				if finalItemName == "" {
					finalItemName = "Barang Manual"
				}

			} else {
				// 🔥 LOGIKA BARANG NORMAL KATALOG
				var branchStock BranchStock
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("branch_id = ? AND product_id = ?", req.BranchID, item.ProductID).First(&branchStock).Error; err != nil {
					if err == gorm.ErrRecordNotFound {
						branchStock = BranchStock{BranchID: req.BranchID, ProductID: item.ProductID, Quantity: 0}
						if err := tx.Create(&branchStock).Error; err != nil {
							return err
						}
					} else {
						return err
					}
				}

				if err := tx.First(&product, item.ProductID).Error; err != nil {
					return err
				}

				actualPrice = product.PriceGeneral
				if req.CustomerID != nil && customer.CustomerType == "toko" {
					actualPrice = product.PriceToko
				}
				if item.BargainedPrice > 0 {
					actualPrice = item.BargainedPrice
				}

				totalModalHPP = product.BasePrice * float64(item.Quantity)
				finalItemName = product.Name

				if err := tx.Model(&BranchStock{}).Where("id = ?", branchStock.ID).
					UpdateColumn("quantity", gorm.Expr("quantity - ?", item.Quantity)).Error; err != nil {
					return err
				}
			}

			subtotal := actualPrice * float64(item.Quantity)
			subTotalAmount += subtotal
			itemProfit := subtotal - totalModalHPP

			txItem := TransactionItem{
				TransactionID:   transaction.ID,
				ProductID:       product.ID,
				ItemName:        finalItemName,
				Quantity:        item.Quantity,
				ActualSoldPrice: actualPrice,
				Subtotal:        subtotal,
				HppTotal:        totalModalHPP,
				Profit:          itemProfit,
				PickupStatus:    "selesai",
			}
			if err := tx.Create(&txItem).Error; err != nil {
				return err
			}
		}

		taxAmount := subTotalAmount * (branchSettings.TaxPercent / 100)
		finalGrandTotal := subTotalAmount + taxAmount

		if err := tx.Model(&transaction).Update("total_amount", finalGrandTotal).Error; err != nil {
			return err
		}

		// 🔥 PENCATATAN KEUANGAN CERDAS (LUNAS / KASBON / SPLIT)
		// 🔥 PENCATATAN KEUANGAN CERDAS DENGAN TAGGED SWITCH
		switch req.PaymentMethod {
		case "kasbon":
			// Skenario 1: Full Ngutang (Tanpa DP)
			if req.CustomerID == nil {
				return fmt.Errorf("Transaksi kasbon wajib memilih Pelanggan yang terdaftar!")
			}
			if err := tx.Model(&Customer{}).Where("id = ?", req.CustomerID).
				UpdateColumn("current_debt", gorm.Expr("current_debt + ?", finalGrandTotal)).Error; err != nil {
				return err
			}

		case "split":
			// Skenario 2: Split Payment (Kombinasi Metode Lunas / Kasbon DP)
			if req.CustomerID != nil && req.DownPayment < finalGrandTotal {
				// 2A. Skenario KASBON + DP
				sisaHutang := finalGrandTotal - req.DownPayment
				
				if req.DownPayment > 0 {
					cashIn := CashLedger{
						BranchID:        req.BranchID,
						WalletType:      "CASH",
						TransactionType: "IN",
						Category:        "Penjualan",
						Description:     fmt.Sprintf("DP Penjualan Kasbon (%s) - INV-%d", customer.Name, transaction.ID),
						Amount:          req.DownPayment,
						CreatedBy:       req.CashierID,
					}
					if err := tx.Create(&cashIn).Error; err != nil { return err }

					dpRecord := DebtPayment{
						CustomerID:    *req.CustomerID,
						AmountPaid:    req.DownPayment,
						PaymentMethod: "tunai",
						ReceiverName:  "Admin Kasir",
						BranchID:      req.BranchID,
					}
					if err := tx.Create(&dpRecord).Error; err != nil { return err }
				}

				if err := tx.Model(&Customer{}).Where("id = ?", req.CustomerID).
					UpdateColumn("current_debt", gorm.Expr("current_debt + ?", sisaHutang)).Error; err != nil { return err }

			} else {
				// 2B. Skenario LUNAS KOMBINASI (Misal: Cash + TF + QRIS)
				cashIn := CashLedger{
					BranchID:        req.BranchID,
					WalletType:      "MIXED",
					TransactionType: "IN",
					Category:        "Penjualan",
					Description:     fmt.Sprintf("Penjualan Kasir (Kombinasi) - INV-%d", transaction.ID),
					Amount:          finalGrandTotal,
					CreatedBy:       req.CashierID,
				}
				if err := tx.Create(&cashIn).Error; err != nil { return err }
			}

		default:
			// Skenario 3: Lunas Biasa Tunggal (Tunai / Transfer / QRIS)
			walletType := "CASH"
			methodLabel := "Tunai"

			switch req.PaymentMethod {
			case "transfer":
				walletType = "BANK"
				methodLabel = "Transfer Bank"
			case "qris":
				walletType = "BANK"
				methodLabel = "QRIS"
			}

			cashIn := CashLedger{
				BranchID:        req.BranchID,
				WalletType:      walletType,
				TransactionType: "IN",
				Category:        "Penjualan",
				Description:     fmt.Sprintf("Penjualan Kasir (%s) - INV-%d", methodLabel, transaction.ID),
				Amount:          finalGrandTotal,
				CreatedBy:       req.CashierID,
			}
			if err := tx.Create(&cashIn).Error; err != nil { return err }
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Transaksi gagal: " + err.Error()})
		return
	}

	go func() {
		broadcast <- map[string]interface{}{"type": "NEW_TRANSACTION", "waktu": time.Now().Format("15:04:05")}
		broadcast <- map[string]interface{}{"type": "STOCK_UPDATE", "waktu": time.Now().Format("15:04:05")}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Pembayaran " + req.PaymentMethod + " sukses!"})
}

// ==========================================
// MODUL PELANGGAN & MANAJEMEN KASBON
// ==========================================

func createCustomer(c *gin.Context) {
	var input struct {
		Name         string  `json:"name"`
		Phone        string  `json:"phone"`
		Address      string  `json:"address"`
		CustomerType string  `json:"customer_type"`
		CreditLimit  float64 `json:"credit_limit"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	if input.CustomerType == "" {
		input.CustomerType = "umum"
	}

	customer := Customer{
		Name:         input.Name,
		Phone:        input.Phone,
		Address:      input.Address,
		CustomerType: input.CustomerType,
		CreditLimit:  input.CreditLimit,
		CurrentDebt:  0,
		CreatedAt:    time.Now(),
	}

	if err := DB.Create(&customer).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan pelanggan"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Pelanggan berhasil ditambahkan!", "data": customer})
}

func getCustomers(c *gin.Context) {
	type CustomerResponse struct {
		ID           int       `json:"id"`
		Name         string    `json:"name"`
		Phone        string    `json:"phone"`
		Address      string    `json:"address"`
		CustomerType string    `json:"customer_type"`
		CreditLimit  float64   `json:"credit_limit"`
		CurrentDebt  float64   `json:"current_debt"`
		CreatedAt    time.Time `json:"created_at"`
	}

	var customers []Customer
	if err := DB.Find(&customers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik data Pelanggan"})
		return
	}

	var response []CustomerResponse
	for _, m := range customers {
		var totalKasbon, totalDibayar float64
		DB.Model(&Transaction{}).Where("customer_id = ? AND payment_method = ?", m.ID, "kasbon").Select("COALESCE(SUM(total_amount), 0)").Scan(&totalKasbon)
		DB.Model(&DebtPayment{}).Where("customer_id = ?", m.ID).Select("COALESCE(SUM(amount_paid), 0)").Scan(&totalDibayar)

		sisaHutang := totalKasbon - totalDibayar

		response = append(response, CustomerResponse{
			ID:           m.ID,
			Name:         m.Name,
			Phone:        m.Phone,
			Address:      m.Address,
			CustomerType: m.CustomerType,
			CreditLimit:  m.CreditLimit,
			CurrentDebt:  sisaHutang,
			CreatedAt:    m.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

func updateCustomer(c *gin.Context) {
	customerID := c.Param("id")
	var input struct {
		Name         string  `json:"name"`
		Phone        string  `json:"phone"`
		Address      string  `json:"address"`
		CustomerType string  `json:"customer_type"`
		CreditLimit  float64 `json:"credit_limit"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	if err := DB.Model(&Customer{}).Where("id = ?", customerID).Updates(Customer{
		Name:         input.Name,
		Phone:        input.Phone,
		Address:      input.Address,
		CustomerType: input.CustomerType,
		CreditLimit:  input.CreditLimit,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mengupdate pelanggan"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Data Pelanggan berhasil diperbarui!"})
}

func deleteCustomer(c *gin.Context) {
	customerID := c.Param("id")

	var totalKasbon, totalDibayar float64
	DB.Model(&Transaction{}).Where("customer_id = ? AND payment_method = ?", customerID, "kasbon").Select("COALESCE(SUM(total_amount), 0)").Scan(&totalKasbon)
	DB.Model(&DebtPayment{}).Where("customer_id = ?", customerID).Select("COALESCE(SUM(amount_paid), 0)").Scan(&totalDibayar)

	if (totalKasbon - totalDibayar) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dilarang menghapus! Pelanggan masih memiliki hutang kasbon berjalan."})
		return
	}

	// GORM otomatis Soft Delete berkat kolom DeletedAt
	if err := DB.Where("id = ?", customerID).Delete(&Customer{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus pelanggan"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Pelanggan berhasil dihapus!"})
}

func getKasbonList(c *gin.Context) {
	type KasbonResponse struct {
		CustomerID   int       `json:"customer_id"`
		Name         string    `json:"name"`
		Phone        string    `json:"phone"`
		Address      string    `json:"address"`
		CustomerType string    `json:"customer_type"`
		LastDate     time.Time `json:"last_date"`
		TotalKasbon  float64   `json:"total_kasbon"`
		Dibayar      float64   `json:"dibayar"`
		Sisa         float64   `json:"sisa"`
		Status       string    `json:"status"`
	}

	var customers []Customer
	if err := DB.Find(&customers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik data Pelanggan"})
		return
	}

	var response []KasbonResponse

	for _, m := range customers {
		var totalKasbon, totalDibayar float64
		var lastTx Transaction

		DB.Model(&Transaction{}).Where("customer_id = ? AND payment_method = ?", m.ID, "kasbon").Select("COALESCE(SUM(total_amount), 0)").Scan(&totalKasbon)
		DB.Model(&DebtPayment{}).Where("customer_id = ?", m.ID).Select("COALESCE(SUM(amount_paid), 0)").Scan(&totalDibayar)
		DB.Where("customer_id = ? AND payment_method = ?", m.ID, "kasbon").Order("created_at desc").First(&lastTx)

		sisa := totalKasbon - totalDibayar
		status := "Lunas"
		if totalKasbon > 0 {
			if totalDibayar == 0 {
				status = "Belum Bayar"
			} else if sisa > 0 {
				status = "Sebagian"
			}
		}

		if totalKasbon > 0 {
			response = append(response, KasbonResponse{
				CustomerID:   m.ID,
				Name:         m.Name,
				Phone:        m.Phone,
				Address:      m.Address,
				CustomerType: m.CustomerType,
				LastDate:     lastTx.CreatedAt,
				TotalKasbon:  totalKasbon,
				Dibayar:      totalDibayar,
				Sisa:         sisa,
				Status:       status,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

func payDebt(c *gin.Context) {
	username, exists := c.Get("username")
	receiver := "Admin Pusat"
	if exists {
		receiver = username.(string)
	}

	userID, _ := c.Get("user_id")

	var input struct {
		BranchID      int     `json:"branch_id"`
		CustomerID    int     `json:"customer_id"`
		AmountPaid    float64 `json:"amount_paid"`
		PaymentMethod string  `json:"payment_method"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	// BUNGKUS DENGAN TRANSAKSI DATABASE (AMAN DARI DATA MATI LAMPU)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var customer Customer
		if err := tx.First(&customer, input.CustomerID).Error; err != nil {
			return fmt.Errorf("Pelanggan tidak ditemukan")
		}

		// 1. Kurangi Hutang Pelanggan
		customer.CurrentDebt -= input.AmountPaid
		if customer.CurrentDebt < 0 {
			customer.CurrentDebt = 0
		}
		if err := tx.Save(&customer).Error; err != nil {
			return err
		}

		// 2. Catat Riwayat Pelunasan
		payment := DebtPayment{
			CustomerID:    input.CustomerID,
			AmountPaid:    input.AmountPaid,
			PaymentMethod: input.PaymentMethod,
			ReceiverName:  receiver,
			BranchID:      input.BranchID,
		}
		if err := tx.Create(&payment).Error; err != nil {
			return err
		}

		// 3. Masukkan Uang ke Buku Kas
		walletType := "CASH"
		if input.PaymentMethod != "tunai" {
			walletType = "BANK"
		}
		
		cashIn := CashLedger{
			BranchID:        input.BranchID,
			WalletType:      walletType,
			TransactionType: "IN",
			Category:        "Pelunasan Piutang",
			Description:     fmt.Sprintf("Pelunasan Kasbon a.n %s (via %s)", customer.Name, input.PaymentMethod),
			Amount:          input.AmountPaid,
			CreatedBy:       userID.(int), // 👈 INI YANG SEBELUMNYA HILANG
		}
		
		if err := tx.Create(&cashIn).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memproses pembayaran: " + err.Error()})
		return
	}

	// 4. Update Layar Real-Time
	go func() {
		broadcast <- map[string]interface{}{
			"type":  "CASH_UPDATE",
			"pesan": "Pelunasan Kasbon Masuk",
			"waktu": time.Now().Format("15:04:05"),
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Pembayaran berhasil dicatat & masuk Buku Kas"})
}

func getDebtHistory(c *gin.Context) {
	var payments []struct {
		ID            int       `json:"id"`
		CustomerName  string    `json:"customer_name"`
		AmountPaid    float64   `json:"amount_paid"`
		PaymentMethod string    `json:"payment_method"`
		ReceiverName  string    `json:"receiver_name"`
		CreatedAt     time.Time `json:"created_at"`
	}

	DB.Table("debt_payments").
		Select("debt_payments.id, customers.name as customer_name, debt_payments.amount_paid, debt_payments.payment_method, debt_payments.receiver_name, debt_payments.created_at").
		Joins("left join customers on customers.id = debt_payments.customer_id").
		Order("debt_payments.created_at desc").
		Scan(&payments)

	c.JSON(200, gin.H{"data": payments})
}

// ==========================================
// MODUL BUKU KAS
// ==========================================

func getCashLedger(c *gin.Context) {
	var ledgers []CashLedger
	query := DB.Order("created_at desc").Limit(50)
	
	// Opsional: Filter BranchID 
	if branchID := c.Query("branch_id"); branchID != "" {
		query = query.Where("branch_id = ?", branchID)
	}

	if err := query.Find(&ledgers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik data buku kas"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ledgers})
}

func createCashLedger(c *gin.Context) {
	var input CashLedger
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	userID, _ := c.Get("user_id")
	input.CreatedBy = userID.(int)

	if err := DB.Create(&input).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mencatat arus kas"})
		return
	}

	go func() {
		broadcast <- map[string]interface{}{
			"type":  "CASH_UPDATE",
			"pesan": "Arus Kas Baru: " + input.Description,
			"waktu": time.Now().Format("15:04:05"),
		}
	}()

	c.JSON(http.StatusCreated, gin.H{"message": "Catatan kas berhasil disimpan!", "data": input})
}

// ==========================================
// MODUL AUDIT TRAIL
// ==========================================
type PriceUpdateRequest struct {
	NewPriceGeneral float64 `json:"new_price_general"`
	NewPriceToko    float64 `json:"new_price_toko"`
}

func updateProductPrice(c *gin.Context) {
	productID := c.Param("id")
	var req PriceUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		var product Product
		if err := tx.First(&product, productID).Error; err != nil {
			return fmt.Errorf("Barang tidak ditemukan")
		}

		logEntry := PriceChangeLog{
			ProductID:       product.ID,
			OldPriceGeneral: product.PriceGeneral,
			NewPriceGeneral: req.NewPriceGeneral,
			OldPriceToko:    product.PriceToko,
			NewPriceToko:    req.NewPriceToko,
			ChangedBy:       userID.(int),
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}

		product.PriceGeneral = req.NewPriceGeneral
		product.PriceToko = req.NewPriceToko
		if err := tx.Save(&product).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Harga berhasil diubah & dicatat di log Audit!"})
}

func getProductPriceLogs(c *gin.Context) {
	productID := c.Param("id")

	type PriceLogResponse struct {
		ID              int       `json:"id"`
		ProductID       int       `json:"product_id"`
		OldPriceGeneral float64   `json:"old_price_general"`
		NewPriceGeneral float64   `json:"new_price_general"`
		OldPriceToko    float64   `json:"old_price_toko"`
		NewPriceToko    float64   `json:"new_price_toko"`
		ChangedBy       int       `json:"changed_by"`
		UserName        string    `json:"user_name"`
		CreatedAt       time.Time `json:"created_at"`
		Details         string    `json:"details"`
	}

	var logs []PriceLogResponse

	err := DB.Table("price_change_logs").
		Select("price_change_logs.*, users.username as user_name").
		Joins("left join users on users.id = price_change_logs.changed_by").
		Where("price_change_logs.product_id = ?", productID).
		Order("price_change_logs.created_at desc").
		Scan(&logs).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik riwayat harga"})
		return
	}

	for i := range logs {
		logs[i].Details = fmt.Sprintf("Ubah Harga Umum: Rp %.0f ➔ Rp %.0f | Toko: Rp %.0f ➔ Rp %.0f", logs[i].OldPriceGeneral, logs[i].NewPriceGeneral, logs[i].OldPriceToko, logs[i].NewPriceToko)
	}

	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// 🔥 INI STRUKTUR YANG TADI HILANG UNTUK OPNAME
type StockAdjustRequest struct {
	BranchID    int    `json:"branch_id"`
	ProductID   int    `json:"product_id"`
	NewQuantity int    `json:"new_quantity"`
	Reason      string `json:"reason"`
}

func adjustStock(c *gin.Context) {
	var req StockAdjustRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	if req.Reason == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Alasan penyesuaian wajib diisi!"})
		return
	}

	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		var branchStock BranchStock
		result := tx.Where("branch_id = ? AND product_id = ?", req.BranchID, req.ProductID).First(&branchStock)

		oldQty := 0
		switch result.Error {
		case nil:
			oldQty = branchStock.Quantity
		case gorm.ErrRecordNotFound:
			branchStock = BranchStock{BranchID: req.BranchID, ProductID: req.ProductID, Quantity: 0}
			if err := tx.Create(&branchStock).Error; err != nil {
				return err
			}
		default:
			return result.Error
		}

		difference := req.NewQuantity - oldQty

		logEntry := StockAdjustmentLog{
			BranchID:    req.BranchID,
			ProductID:   req.ProductID,
			OldQuantity: oldQty,
			NewQuantity: req.NewQuantity,
			Difference:  difference,
			Reason:      req.Reason,
			AdjustedBy:  userID.(int),
		}
		if err := tx.Create(&logEntry).Error; err != nil {
			return err
		}

		branchStock.Quantity = req.NewQuantity
		if err := tx.Save(&branchStock).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyesuaikan stok: " + err.Error()})
		return
	}

	go func() {
		broadcast <- map[string]interface{}{"type": "STOCK_UPDATE", "pesan": "Opname Stok dilakukan."}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Stok berhasil disesuaikan & jejak terekam!"})
}

func getStockAdjustments(c *gin.Context) {
	type StockLogResponse struct {
		ID          int       `json:"id"`
		BranchID    int       `json:"branch_id"`
		ProductID   int       `json:"product_id"`
		OldQuantity int       `json:"old_quantity"`
		NewQuantity int       `json:"new_quantity"`
		Difference  int       `json:"difference"`
		Reason      string    `json:"reason"`
		AdjustedBy  int       `json:"adjusted_by"`
		UserName    string    `json:"user_name"`
		CreatedAt   time.Time `json:"created_at"`
	}

	var logs []StockLogResponse

	err := DB.Table("stock_adjustment_logs").
		Select("stock_adjustment_logs.*, users.username as user_name").
		Joins("left join users on users.id = stock_adjustment_logs.adjusted_by").
		Order("stock_adjustment_logs.created_at desc").
		Scan(&logs).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik riwayat opname stok"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// ==========================================
// MODUL DASHBOARD
// ==========================================
type TopProduct struct {
	Name      string `json:"name"`
	TotalSold int    `json:"total_sold"`
}

type DashboardData struct {
	TotalOmzetHariIni  float64      `json:"total_omzet_hari_ini"`
	TotalPiutang       float64      `json:"total_piutang_berjalan"`
	TotalPengeluaran   float64      `json:"total_pengeluaran_hari_ini"`
	TotalLabaHariIni   float64      `json:"total_laba_hari_ini"` 
	Top5Products       []TopProduct `json:"top_5_products"`
	DeadStocks         []Product    `json:"dead_stocks"`
}

func getDashboard(c *gin.Context) {
	var data DashboardData
	today := time.Now().Format("2006-01-02")
	branchID := c.Query("branch_id")

	// Filter cabang dinamis Omzet
	txOmzet := DB.Model(&Transaction{}).Where("DATE(created_at) = ?", today)
	if branchID != "" { txOmzet = txOmzet.Where("branch_id = ?", branchID) }
	txOmzet.Select("COALESCE(SUM(total_amount), 0)").Scan(&data.TotalOmzetHariIni)

	// Filter cabang dinamis Pengeluaran
	txKeluar := DB.Model(&CashLedger{}).Where("DATE(created_at) = ? AND transaction_type = ?", today, "OUT")
	if branchID != "" { txKeluar = txKeluar.Where("branch_id = ?", branchID) }
	txKeluar.Select("COALESCE(SUM(amount), 0)").Scan(&data.TotalPengeluaran)

	// Piutang
	var totalKasbon, totalDibayar float64
	DB.Model(&Transaction{}).Where("payment_method = ?", "kasbon").Select("COALESCE(SUM(total_amount), 0)").Scan(&totalKasbon)
	DB.Model(&DebtPayment{}).Select("COALESCE(SUM(amount_paid), 0)").Scan(&totalDibayar)
	data.TotalPiutang = totalKasbon - totalDibayar

	// LOGIKA LABA: Mengambil langsung nilai profit yang sudah dikunci oleh Kasir
	txLaba := DB.Table("transaction_items").
		Select("COALESCE(SUM(transaction_items.profit), 0)").
		Joins("JOIN transactions t ON t.id = transaction_items.transaction_id").
		Where("DATE(t.created_at) = ?", today)
	if branchID != "" { txLaba = txLaba.Where("t.branch_id = ?", branchID) }
	txLaba.Scan(&data.TotalLabaHariIni)

	// Top Produk
	txTop := DB.Table("transaction_items").
		Select("products.name, SUM(transaction_items.quantity) as total_sold").
		Joins("JOIN products ON products.id = transaction_items.product_id").
		Joins("JOIN transactions t ON t.id = transaction_items.transaction_id")
	if branchID != "" { txTop = txTop.Where("t.branch_id = ?", branchID) }
	txTop.Group("products.id, products.name").
		Order("total_sold DESC").
		Limit(5).
		Scan(&data.Top5Products)

	// Dead Stock
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	DB.Raw(`
		SELECT * FROM products 
		WHERE id NOT IN (
			SELECT DISTINCT product_id FROM transaction_items ti
			JOIN transactions t ON t.id = ti.transaction_id
			WHERE t.created_at >= ?
		)
	`, thirtyDaysAgo).Scan(&data.DeadStocks)

	c.JSON(http.StatusOK, gin.H{"message": "Data Dashboard Berhasil Ditarik!", "data": data})
}

// ==========================================
// MODUL HRD & PENGGAJIAN
// ==========================================

func getEmployees(c *gin.Context) {
	var employees []Employee
	query := DB.Order("created_at desc")
	if branchID := c.Query("branch_id"); branchID != "" {
		query = query.Where("branch_id = ?", branchID)
	}
	if err := query.Find(&employees).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik data pegawai"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": employees})
}

func createEmployee(c *gin.Context) {
	var input Employee
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	if err := DB.Create(&input).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan data pegawai"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "Pegawai berhasil ditambahkan!"})
}

func paySalary(c *gin.Context) {
	var req struct {
		BranchID   int     `json:"branch_id"`
		EmployeeID int     `json:"employee_id"`
		Period     string  `json:"period"`
		Amount     float64 `json:"amount"`
		WalletType string  `json:"wallet_type"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data pembayaran salah"})
		return
	}

	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		var emp Employee
		if err := tx.First(&emp, req.EmployeeID).Error; err != nil {
			return fmt.Errorf("data pegawai tidak ditemukan")
		}

		desc := fmt.Sprintf("Gaji Staff: %s (Periode: %s)", emp.Role, req.Period)

		var existing WagePayment
		if err := tx.Where("worker_name = ? AND worker_type = 'staff' AND description = ?", emp.Name, desc).First(&existing).Error; err == nil {
			return fmt.Errorf("gagal! gaji untuk %s periode %s sudah dibayarkan sebelumnya", emp.Name, req.Period)
		}

		wage := WagePayment{
			BranchID:    req.BranchID,
			WorkerName:  emp.Name,
			WorkerType:  "staff",
			Description: desc,
			Amount:      req.Amount,
			WalletType:  req.WalletType,
			PaidBy:      userID.(int),
		}
		if err := tx.Create(&wage).Error; err != nil {
			return err
		}

		cashOut := CashLedger{
			BranchID:        req.BranchID,
			WalletType:      req.WalletType,
			TransactionType: "OUT",
			Category:        "Gaji Karyawan",
			Description:     fmt.Sprintf("Pembayaran %s a.n %s", desc, emp.Name),
			Amount:          req.Amount,
			CreatedBy:       userID.(int),
		}
		if err := tx.Create(&cashOut).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Gaji berhasil dibayarkan dan kas terpotong!"})
}

func getWages(c *gin.Context) {
	workerType := c.Query("type")
	if workerType == "" {
		workerType = "kuli"
	}

	type WageResponse struct {
		ID          int       `json:"id"`
		WorkerName  string    `json:"worker_name"`
		Description string    `json:"description"`
		Amount      float64   `json:"amount"`
		WalletType  string    `json:"wallet_type"`
		PaymentDate time.Time `json:"payment_date"`
		PayerName   string    `json:"payer_name"`
	}

	var results []WageResponse

	query := DB.Table("wage_payments").
		Select("wage_payments.*, users.username as payer_name").
		Joins("left join users on users.id = wage_payments.paid_by").
		Where("wage_payments.worker_type = ?", workerType)

	if branchID := c.Query("branch_id"); branchID != "" {
		query = query.Where("wage_payments.branch_id = ?", branchID)
	}

	err := query.Order("wage_payments.payment_date desc").Scan(&results).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik riwayat upah"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

func payWages(c *gin.Context) {
	var req struct {
		BranchID   int     `json:"branch_id"`
		Name       string  `json:"name"`
		JobDesc    string  `json:"job_desc"`
		Amount     float64 `json:"amount"`
		WalletType string  `json:"wallet_type"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data upah salah"})
		return
	}

	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		wage := WagePayment{
			BranchID:    req.BranchID,
			WorkerName:  req.Name,
			WorkerType:  "kuli",
			Description: req.JobDesc,
			Amount:      req.Amount,
			WalletType:  req.WalletType,
			PaidBy:      userID.(int),
		}
		if err := tx.Create(&wage).Error; err != nil {
			return err
		}

		cashOut := CashLedger{
			BranchID:        req.BranchID,
			WalletType:      req.WalletType,
			TransactionType: "OUT",
			Category:        "Upah Kuli",
			Description:     fmt.Sprintf("Upah Kuli: %s (%s)", req.JobDesc, req.Name),
			Amount:          req.Amount,
			CreatedBy:       userID.(int),
		}
		if err := tx.Create(&cashOut).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memproses upah kuli: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Upah kuli berhasil dibayarkan dan kas terpotong!"})
}

// ==========================================
// MODUL BULK UPLOAD EXCEL
// ==========================================
func bulkUploadProducts(c *gin.Context) {
	// 1. Terima file dari request Frontend
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Gagal membaca file excel"})
		return
	}
	defer file.Close()

	// 2. Buka file menggunakan Excelize
	f, err := excelize.OpenReader(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Format file excel tidak valid"})
		return
	}

	// Ambil nama sheet pertama
	sheetName := f.GetSheetList()[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membaca baris di Excel"})
		return
	}

	var successCount int

	// 3. Eksekusi dalam Transaksi Database (Aman)
	err = DB.Transaction(func(tx *gorm.DB) error {
		for i, row := range rows {
			// Lewati baris pertama (Header: Nama Barang, Kode, Kategori, dll)
			if i == 0 {
				continue
			}

			// Pastikan baris memiliki data (minimal 6 kolom sesuai format Excel-mu)
			if len(row) < 6 {
				continue
			}

			namaBarang := row[0]
			// kodeBarang := row[1] // Kita lewati, karena di struct Product belum ada kolom Kode (Tidak masalah untuk MVP)
			namaKategori := row[2]
			satuan := row[3]
			hargaModalStr := row[4]
			hargaJualStr := row[5]

			// Parsing teks harga menjadi float64
			hargaModal, _ := strconv.ParseFloat(hargaModalStr, 64)
			hargaJual, _ := strconv.ParseFloat(hargaJualStr, 64)

			// Cari Kategori, jika belum ada, buat otomatis
			var category Category
			if err := tx.Where("name = ?", namaKategori).FirstOrCreate(&category, Category{Name: namaKategori}).Error; err != nil {
				return err
			}

			// Masukkan data barang ke database
			product := Product{
				Name:         namaBarang,
				Unit:         satuan,
				PriceGeneral: hargaJual,
				PriceToko:    hargaJual - (hargaJual * 0.10), // Otomatis diskon 10% untuk harga toko
				BasePrice:    hargaModal,                     // HPP dari Excel
				CategoryID:   category.ID,
			}

			if err := tx.Create(&product).Error; err != nil {
				return err // Batalkan semua jika ada 1 yang gagal
			}
			successCount++
		}
		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal import data ke database: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Sukses! %d barang berhasil dimasukkan ke katalog.", successCount)})
}
// ==========================================
// MODUL RIWAYAT TRANSAKSI (BONGKAR LABA)
// ==========================================
func getTransactions(c *gin.Context) {
	branchID := c.Query("branch_id")
	if branchID == "" {
		branchID = "1"
	}

	type TransactionItemDetail struct {
		ItemName string  `json:"item_name"`
		Quantity int     `json:"quantity"`
		Subtotal float64 `json:"subtotal"`
		HppTotal float64 `json:"hpp_total"`
		Profit   float64 `json:"profit"`
	}

	type TransactionResponse struct {
		ID            int                     `json:"id"`
		CustomerName  string                  `json:"customer_name"`
		TotalAmount   float64                 `json:"total_amount"`
		PaymentMethod string                  `json:"payment_method"`
		CreatedAt     time.Time               `json:"created_at"`
		TotalProfit   float64                 `json:"total_profit"`
		Items         []TransactionItemDetail `json:"items"`
	}

	var transactions []Transaction
	// Preload Customer agar nama pelanggan ikut terbawa
	if err := DB.Preload("Customer").Where("branch_id = ?", branchID).Order("created_at desc").Limit(100).Find(&transactions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menarik data riwayat"})
		return
	}

	var response []TransactionResponse

	for _, tx := range transactions {
		var items []TransactionItem
		DB.Where("transaction_id = ?", tx.ID).Find(&items)

		var itemDetails []TransactionItemDetail
		var totalProfit float64

		for _, item := range items {
			itemDetails = append(itemDetails, TransactionItemDetail{
				ItemName: item.ItemName,
				Quantity: item.Quantity,
				Subtotal: item.Subtotal,
				HppTotal: item.HppTotal,
				Profit:   item.Profit,
			})
			totalProfit += item.Profit
		}

		// 🔥 PERBAIKAN: Karena tx.Customer adalah Struct, kita cek apakah ID atau Namanya kosong
		custName := "UMUM"
		if tx.Customer.Name != "" {
			custName = tx.Customer.Name
		}

		response = append(response, TransactionResponse{
			ID:            tx.ID,
			CustomerName:  custName,
			TotalAmount:   tx.TotalAmount,
			PaymentMethod: tx.PaymentMethod,
			CreatedAt:     tx.CreatedAt,
			TotalProfit:   totalProfit,
			Items:         itemDetails,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}
// ==========================================
// MODUL RETUR / BATALKAN NOTA (VOID)
// ==========================================
func voidTransaction(c *gin.Context) {
	txID := c.Param("id")
	userID, _ := c.Get("user_id")

	err := DB.Transaction(func(tx *gorm.DB) error {
		var transaction Transaction
		if err := tx.First(&transaction, txID).Error; err != nil {
			return fmt.Errorf("Transaksi tidak ditemukan")
		}

		if transaction.PaymentMethod == "BATAL/VOID" {
			return fmt.Errorf("Transaksi sudah dibatalkan sebelumnya")
		}

		// 1. KEMBALIKAN FISIK STOK KE GUDANG
		var items []TransactionItem
		tx.Where("transaction_id = ?", transaction.ID).Find(&items)

		for _, item := range items {
			if item.ProductID > 0 {
				tx.Model(&BranchStock{}).Where("branch_id = ? AND product_id = ?", transaction.BranchID, item.ProductID).
					UpdateColumn("quantity", gorm.Expr("quantity + ?", item.Quantity))
			}
		}

		// 2. KEMBALIKAN KEUANGAN (TARIK DARI LACI ATAU HAPUS PIUTANG)
		if transaction.PaymentMethod == "kasbon" {
			tx.Model(&Customer{}).Where("id = ?", transaction.CustomerID).
				UpdateColumn("current_debt", gorm.Expr("current_debt - ?", transaction.TotalAmount))

		} else if transaction.PaymentMethod == "split" {
			// Cari catatan DP di Kas Ledger
			var cl CashLedger
			tx.Where("description LIKE ?", fmt.Sprintf("%%INV-%d", transaction.ID)).First(&cl)

			dp := 0.0
			if cl.ID != 0 {
				dp = cl.Amount
				// Retur DP
				returCash := CashLedger{
					BranchID: transaction.BranchID, WalletType: cl.WalletType, TransactionType: "OUT",
					Category: "Retur / Void", Description: fmt.Sprintf("Retur Batal Nota INV-%d", transaction.ID),
					Amount: dp, CreatedBy: userID.(int),
				}
				tx.Create(&returCash)
			}
			
			sisaHutang := transaction.TotalAmount - dp
			tx.Model(&Customer{}).Where("id = ?", transaction.CustomerID).
				UpdateColumn("current_debt", gorm.Expr("current_debt - ?", sisaHutang))

		} else {
			// Tarik Uang dari Laci Kasir / Rekening
			returCash := CashLedger{
				BranchID: transaction.BranchID, WalletType: "CASH", TransactionType: "OUT",
				Category: "Retur / Void", Description: fmt.Sprintf("Retur Batal Nota INV-%d", transaction.ID),
				Amount: transaction.TotalAmount, CreatedBy: userID.(int),
			}
			tx.Create(&returCash)
		}

		// 3. TANDAI TRANSAKSI MENJADI VOID (Bukan dihapus agar jejaknya ada)
		return tx.Model(&transaction).Update("payment_method", "BATAL/VOID").Error
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	go func() {
		broadcast <- map[string]interface{}{"type": "NEW_TRANSACTION", "pesan": "Nota Dibatalkan"}
		broadcast <- map[string]interface{}{"type": "STOCK_UPDATE", "pesan": "Stok Retur Masuk"}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Nota berhasil dibatalkan dan stok telah dikembalikan ke gudang!"})
}

// ==========================================
// 🔥 MODUL SHIFT CLOSING & 5 BLOK EOD (Fase 3 - Snapshot & Timezone)
// ==========================================

func submitShiftClosing(c *gin.Context) {
	var input ShiftClosingRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format data salah"})
		return
	}

	userID, _ := c.Get("user_id")

	// 🔥 Paksa Waktu ke Zona Asia/Jakarta (WIB)
	loc, _ := time.LoadLocation("Asia/Jakarta")
	waktuLokal := time.Now().In(loc)

	closing := ShiftClosing{
		BranchID:           input.BranchID,
		CashierID:          userID.(int),
		ClosingDate:        waktuLokal,
		ExpectedCash:       input.ExpectedCash,
		ActualPhysicalCash: input.ActualPhysicalCash,
		Difference:         input.Difference,
		TotalOmzet:         input.TotalOmzet,
		TotalProfit:        input.TotalProfit,
		SnapshotData:       input.SnapshotData,
		CreatedAt:          waktuLokal,
	}

	if err := DB.Create(&closing).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan laporan tutup buku"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Shift berhasil ditutup dan data dibekukan (Snapshot)"})
}

func getShiftSummary(c *gin.Context) {
	branchID := c.Query("branch_id")
	if branchID == "" {
		branchID = "1"
	}

	loc, _ := time.LoadLocation("Asia/Jakarta")

	// 1. Cari Waktu Tutup Buku Terakhir
	var lastClose ShiftClosing
	DB.Where("branch_id = ?", branchID).Order("created_at desc").First(&lastClose)

	lastCloseTime := lastClose.CreatedAt
	if lastClose.ID == 0 {
		now := time.Now().In(loc)
		// Jika belum pernah tutup buku, ambil transaksi dari jam 00:00 hari ini WIB
		lastCloseTime = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	}

	// 2. Tarik Arus Kas (Hanya setelah waktu Tutup Terakhir)
	var ledgers []CashLedger
	DB.Where("branch_id = ? AND created_at > ?", branchID, lastCloseTime).Find(&ledgers)

	var jualTunai, bayarKasbonTunai, keluarTunai, jualBank, bayarKasbonBank float64
	type ExpenseDetail struct {
		Description string  `json:"description"`
		Amount      float64 `json:"amount"`
	}
	var expenses []ExpenseDetail

	for _, l := range ledgers {
		if l.WalletType == "CASH" {
			if l.TransactionType == "IN" {
				if l.Category == "Penjualan" {
					jualTunai += l.Amount
				} else if l.Category == "Pelunasan Piutang" {
					bayarKasbonTunai += l.Amount
				}
			} else if l.TransactionType == "OUT" {
				keluarTunai += l.Amount
				expenses = append(expenses, ExpenseDetail{Description: l.Description, Amount: l.Amount})
			}
		} else {
			if l.TransactionType == "IN" {
				if l.Category == "Penjualan" {
					jualBank += l.Amount
				} else if l.Category == "Pelunasan Piutang" {
					bayarKasbonBank += l.Amount
				}
			}
		}
	}

	// 3. Tarik Kasbon Baru & Pelunasan
	type DebtDetail struct {
		CustomerName string  `json:"customer_name"`
		Description  string  `json:"description"`
		Amount       float64 `json:"amount"`
	}
	var newKasbons []DebtDetail
	var kasbonPayments []DebtDetail

	DB.Table("transactions").
		Select("customers.name as customer_name, 'Nota INV-' || CAST(transactions.id AS VARCHAR) as description, transactions.total_amount as amount").
		Joins("JOIN customers ON customers.id = transactions.customer_id").
		Where("transactions.branch_id = ? AND transactions.created_at > ? AND transactions.payment_method = 'kasbon'", branchID, lastCloseTime).
		Scan(&newKasbons)

	DB.Table("debt_payments").
		Select("customers.name as customer_name, 'Via ' || debt_payments.payment_method as description, debt_payments.amount_paid as amount").
		Joins("JOIN customers ON customers.id = debt_payments.customer_id").
		Where("debt_payments.branch_id = ? AND debt_payments.created_at > ?", branchID, lastCloseTime).
		Scan(&kasbonPayments)

	// 4. Tarik Rekap Barang Laku & Total Laba Bersih
	type SoldItem struct {
		ItemName string `json:"item_name"`
		Qty      int    `json:"qty"`
	}
	var soldItems []SoldItem
	var totalProfit float64

	// Grouping Barang Terjual
	DB.Table("transaction_items").
		Select("transaction_items.item_name, SUM(transaction_items.quantity) as qty").
		Joins("JOIN transactions on transactions.id = transaction_items.transaction_id").
		Where("transactions.branch_id = ? AND transactions.created_at > ? AND transactions.payment_method != 'BATAL/VOID'", branchID, lastCloseTime).
		Group("transaction_items.item_name").Order("qty DESC").
		Scan(&soldItems)

	// Hitung Laba Bersih Total di Shift Ini
	DB.Table("transaction_items").
		Select("COALESCE(SUM(transaction_items.profit), 0)").
		Joins("JOIN transactions on transactions.id = transaction_items.transaction_id").
		Where("transactions.branch_id = ? AND transactions.created_at > ? AND transactions.payment_method != 'BATAL/VOID'", branchID, lastCloseTime).
		Scan(&totalProfit)

	// Kirim Paket 5 Blok ke React
	c.JSON(http.StatusOK, gin.H{
		"last_close":                  lastCloseTime,
		"penjualan_tunai":             jualTunai,
		"pembayaran_kasbon_tunai":     bayarKasbonTunai,
		"pengeluaran":                 keluarTunai,
		"penjualan_non_tunai":         jualBank,
		"pembayaran_kasbon_non_tunai": bayarKasbonBank,
		"expense_details":             expenses,
		"new_kasbon":                  newKasbons,
		"kasbon_payments":             kasbonPayments,
		"sold_items":                  soldItems,
		"total_profit":                totalProfit,
	})
}

func getShiftHistory(c *gin.Context) {
	branchID := c.Query("branch_id")
	var history []ShiftClosing
	// Mengurutkan riwayat dari yang paling baru
	DB.Where("branch_id = ?", branchID).Order("created_at desc").Limit(30).Find(&history)
	c.JSON(http.StatusOK, gin.H{"data": history})
}